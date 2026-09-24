package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// connectAuthedTestWS opens a real WebSocket connection to testServer,
// authenticated as the shared integration-test user, and waits for auth_ack.
// It mirrors TestWebSocketIntegration's dial/auth sequence so each call can
// stand in for one physical device (e.g. "PC" vs "mobile") of the same user.
func connectAuthedTestWS(t *testing.T) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws?workspace_id=" + testWorkspaceID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	authMsg, _ := json.Marshal(map[string]any{
		"type":    "auth",
		"payload": map[string]string{"token": testToken},
	})
	if err := conn.WriteMessage(websocket.TextMessage, authMsg); err != nil {
		conn.Close()
		t.Fatalf("write auth: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, ack, err := conn.ReadMessage(); err != nil || !strings.Contains(string(ack), "auth_ack") {
		conn.Close()
		t.Fatalf("auth_ack: err=%v ack=%s", err, ack)
	}
	conn.SetReadDeadline(time.Time{})
	return conn
}

// readInboxArchivedFrame reads WS frames off conn until it finds an
// inbox:archived frame (skipping unrelated broadcast noise from the shared
// integration workspace) or the deadline is hit.
func readInboxArchivedFrame(t *testing.T, conn *websocket.Conn, deadline time.Time) map[string]any {
	t.Helper()
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timed out waiting for inbox:archived frame")
		}
		conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("WebSocket read error waiting for inbox:archived: %v", err)
		}
		var frame map[string]any
		if err := json.Unmarshal(msg, &frame); err != nil {
			continue
		}
		if frame["type"] == "inbox:archived" {
			return frame
		}
	}
}

// TestInboxArchive_DeliversToAllConnectionsOfRecipient reproduces RUYI-172 at
// the server layer: archiving an inbox item on one device (a real WS
// connection standing in for the PC/web client) must fan the inbox:archived
// event out to every other live WS connection the SAME recipient holds (a
// second real connection standing in for the mobile client), through the
// real HTTP handler, real event bus, and real realtime.Hub end to end.
func TestInboxArchive_DeliversToAllConnectionsOfRecipient(t *testing.T) {
	// Two simultaneous connections for the SAME user simulate PC + mobile.
	pcConn := connectAuthedTestWS(t)
	defer pcConn.Close()
	mobileConn := connectAuthedTestWS(t)
	defer mobileConn.Close()

	// Let the Hub goroutine finish registering both connections in the
	// user/workspace rooms before we create anything.
	time.Sleep(100 * time.Millisecond)

	// Create an issue, then a real inbox_item row addressed to the test user
	// for that issue (mirrors how task/notification flows populate inbox_item).
	resp := authRequest(t, "POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "RUYI-172 inbox archive fanout",
		"status": "todo",
	})
	var issue map[string]any
	readJSON(t, resp, &issue)
	issueID := issue["id"].(string)

	var itemID string
	if err := testPool.QueryRow(t.Context(), `
		INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title)
		VALUES ($1, 'member', $2, 'status_changed', 'info', $3, 'RUYI-172 inbox archive fanout')
		RETURNING id
	`, testWorkspaceID, testUserID, issueID).Scan(&itemID); err != nil {
		t.Fatalf("insert inbox_item: %v", err)
	}

	// Drain the issue:created broadcast (and any other pre-archive noise) on
	// both connections before triggering the archive under test.
	drainDeadline := time.Now().Add(1 * time.Second)
	for _, c := range []*websocket.Conn{pcConn, mobileConn} {
		c.SetReadDeadline(drainDeadline)
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				break
			}
			var frame map[string]any
			if json.Unmarshal(msg, &frame) == nil && frame["type"] == "issue:created" {
				break
			}
		}
		c.SetReadDeadline(time.Time{})
	}

	// Archive from the "PC" side via the real HTTP handler.
	archiveResp := authRequest(t, "POST", "/api/inbox/"+itemID+"/archive", nil)
	archiveResp.Body.Close()
	if archiveResp.StatusCode != 200 {
		t.Fatalf("archive request status = %d, want 200", archiveResp.StatusCode)
	}

	deadline := time.Now().Add(3 * time.Second)

	// The archiving connection itself sees the event (baseline sanity).
	pcFrame := readInboxArchivedFrame(t, pcConn, deadline)
	if got, _ := pcFrame["payload"].(map[string]any)["item_id"].(string); got != itemID {
		t.Fatalf("pc payload item_id = %q, want %q", got, itemID)
	}

	// The OTHER live connection for the same recipient (mobile) must also
	// receive inbox:archived without any manual refresh — this is exactly
	// acceptance criterion 1 (web→mobile) from RUYI-172.
	mobileFrame := readInboxArchivedFrame(t, mobileConn, deadline)
	if got, _ := mobileFrame["payload"].(map[string]any)["item_id"].(string); got != itemID {
		t.Fatalf("mobile payload item_id = %q, want %q", got, itemID)
	}

	// Cold-start / fresh-fetch: the archived item must never reappear in the
	// main inbox list (acceptance criterion 2).
	var listResp []map[string]any
	r := authRequest(t, "GET", "/api/inbox", nil)
	readJSON(t, r, &listResp)
	for _, it := range listResp {
		if it["id"] == itemID {
			t.Fatalf("archived item %s still present in GET /api/inbox", itemID)
		}
	}
}

// TestInboxArchive_SiblingRowsForSameIssueAllDisappear probes the
// issue-level archive semantics ArchiveInboxItem relies on: archiving one
// inbox_item row for an issue must archive every other row addressed to the
// same recipient for that issue (acceptance criterion 3), on a cold /
// fresh GET /api/inbox fetch — independent of live WS delivery. This isolates
// the server-persistence layer from the event-delivery layer per RUYI-172's
// no-merged-attribution requirement.
func TestInboxArchive_SiblingRowsForSameIssueAllDisappear(t *testing.T) {
	resp := authRequest(t, "POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "RUYI-172 sibling archive",
		"status": "todo",
	})
	var issue map[string]any
	readJSON(t, resp, &issue)
	issueID := issue["id"].(string)

	insertItem := func(title string) string {
		var id string
		if err := testPool.QueryRow(t.Context(), `
			INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title)
			VALUES ($1, 'member', $2, 'status_changed', 'info', $3, $4)
			RETURNING id
		`, testWorkspaceID, testUserID, issueID, title).Scan(&id); err != nil {
			t.Fatalf("insert inbox_item %q: %v", title, err)
		}
		return id
	}
	firstID := insertItem("RUYI-172 sibling archive - first notification")
	siblingID := insertItem("RUYI-172 sibling archive - second notification")

	archiveResp := authRequest(t, "POST", "/api/inbox/"+firstID+"/archive", nil)
	archiveResp.Body.Close()
	if archiveResp.StatusCode != 200 {
		t.Fatalf("archive request status = %d, want 200", archiveResp.StatusCode)
	}

	var listResp []map[string]any
	r := authRequest(t, "GET", "/api/inbox", nil)
	readJSON(t, r, &listResp)
	for _, it := range listResp {
		if it["id"] == firstID {
			t.Fatalf("archived item %s still present in GET /api/inbox", firstID)
		}
		if it["id"] == siblingID {
			t.Fatalf("sibling item %s for the same issue was not archived and is still present in GET /api/inbox", siblingID)
		}
	}

	var siblingArchived bool
	if err := testPool.QueryRow(t.Context(), `SELECT archived FROM inbox_item WHERE id = $1`, siblingID).Scan(&siblingArchived); err != nil {
		t.Fatalf("query sibling archived state: %v", err)
	}
	if !siblingArchived {
		t.Fatalf("sibling inbox_item %s archived column = false, want true (issue-level archive did not persist)", siblingID)
	}
}

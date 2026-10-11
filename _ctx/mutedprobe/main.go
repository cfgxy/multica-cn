// Dev rework probe (RUYI-694 P1: muted session keepalive). Replicates the
// delivered APK's muted upstream exactly: after setupComplete, every 100ms
// send one 3200-byte all-zero PCM16 frame @16 kHz — byte-for-byte the
// MUTED_KEEPALIVE_CHUNK the mobile fix now emits instead of dropping the
// upstream. Acceptance: the session stays open ≥5min (past the provider
// idle threshold that killed muted sessions at 2m04s/3m00s pre-fix).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type serverFrame struct {
	SetupComplete *json.RawMessage `json:"setupComplete,omitempty"`
	ServerContent *struct {
		InputTranscription *struct {
			Text string `json:"text"`
		} `json:"inputTranscription,omitempty"`
		TurnComplete bool `json:"turnComplete"`
	} `json:"serverContent,omitempty"`
	GoAway *json.RawMessage `json:"goAway,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func main() {
	token := os.Getenv("PROBE_TOKEN")
	agent := os.Getenv("PROBE_AGENT")
	base := os.Getenv("PROBE_BASE")
	if base == "" {
		base = "localhost:21801"
	}
	runFor := 330 * time.Second
	if v := os.Getenv("PROBE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			runFor = time.Duration(n) * time.Second
		}
	}

	// 3200 zero bytes → base64: 1066×"AAAA" + "AAA=" (100ms @16 kHz PCM16),
	// identical to the mobile MUTED_KEEPALIVE_CHUNK constant.
	silentChunk := strings.Repeat("A", 4264) + "AAA="

	url := fmt.Sprintf("ws://%s/api/agents/%s/voice-session?workspace_slug=ruyi694-qa", base, agent)
	hdr := map[string][]string{"Origin": {"http://localhost:13801"}}
	t0 := time.Now()
	el := func() string { return time.Since(t0).Round(time.Millisecond).String() }
	conn, resp, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		if resp != nil {
			fmt.Printf("%s DIAL FAIL http=%d\n", el(), resp.StatusCode)
		} else {
			fmt.Println("DIAL FAIL:", err)
		}
		os.Exit(1)
	}
	fmt.Printf("%s UPGRADED\n", el())

	auth, _ := json.Marshal(map[string]any{"type": "auth", "payload": map[string]string{"token": token}})
	if err := conn.WriteMessage(websocket.TextMessage, auth); err != nil {
		fmt.Println("AUTH WRITE FAIL:", err)
		os.Exit(1)
	}

	inbound := make(chan []byte, 256)
	downErr := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				downErr <- err
				close(inbound)
				return
			}
			inbound <- data
		}
	}()

	setupDone := false
	frames := 0
	deadline := time.Now().Add(runFor)
	for time.Now().Before(deadline) {
		if !setupDone {
			select {
			case data, ok := <-inbound:
				if !ok {
					fmt.Printf("%s DOWN before setupComplete\n", el())
					goto report
				}
				var f serverFrame
				if json.Unmarshal(data, &f) == nil && f.SetupComplete != nil {
					setupDone = true
					fmt.Printf("%s SETUP_COMPLETE — muted keepalive stream begins (100ms × 3200B silence @16k)\n", el())
				}
			case <-time.After(15 * time.Second):
				fmt.Printf("%s TIMEOUT waiting setupComplete\n", el())
				goto report
			}
			continue
		}
		select {
		case data, ok := <-inbound:
			if !ok {
				goto report
			}
			var f serverFrame
			if json.Unmarshal(data, &f) != nil {
				continue
			}
			if f.GoAway != nil {
				fmt.Printf("%s GO_AWAY\n", el())
			}
			if f.Error != nil {
				fmt.Printf("%s ERROR FRAME: %s\n", el(), f.Error.Message)
			}
			if f.ServerContent != nil {
				if f.ServerContent.InputTranscription != nil && f.ServerContent.InputTranscription.Text != "" {
					fmt.Printf("%s IN-TRANSCRIPT: %s\n", el(), f.ServerContent.InputTranscription.Text)
				}
				if f.ServerContent.TurnComplete {
					fmt.Printf("%s TURN_COMPLETE\n", el())
				}
			}
		case <-time.After(100 * time.Millisecond):
			fr, _ := json.Marshal(map[string]any{"realtimeInput": map[string]any{
				"audio": map[string]string{"data": silentChunk, "mimeType": "audio/pcm;rate=16000"}}})
			if err := conn.WriteMessage(websocket.TextMessage, fr); err != nil {
				fmt.Printf("%s KEEPALIVE WRITE FAIL: %v\n", el(), err)
				goto report
			}
			frames++
			if frames%600 == 0 { // every 60s of streamed silence
				fmt.Printf("%s KEEPALIVE frames=%d (~%ds of silence streamed)\n", el(), frames, frames/10)
			}
		}
	}
report:
	alive := false
	select {
	case err := <-downErr:
		fmt.Printf("%s DOWN: %v\n", el(), err)
	default:
		alive = true
	}
	if alive {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(1000, "probe done"), time.Now().Add(2*time.Second))
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Printf("%s RESULT alive=%t keepalive_frames=%d streamed=~%ds (target ≥300s)\n",
		el(), alive, frames, frames/10)
	if !alive || frames < 3000 {
		os.Exit(1)
	}
}

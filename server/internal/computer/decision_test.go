package computer

import (
	"strings"
	"testing"
	"time"
)

func TestDecideOptionA(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want Action
	}{
		{"create", Input{Username: "zhang"}, ActionCreate},
		{"reuse", Input{Username: "zhang", UserExists: true, PasswordOK: true}, ActionReuse},
		{"rename", Input{Username: "zhang", UserExists: true, PasswordOK: false, Failures: 1}, ActionRename},
		{"locked before pam", Input{Username: "zhang", UserExists: true, Failures: 5}, ActionLocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decide(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Action != tc.want {
				t.Fatalf("got %s (%s), want %s", got.Action, got.Reason, tc.want)
			}
		})
	}
}

func TestDecideRejectsBadUsername(t *testing.T) {
	for _, name := range []string{"", "../etc", "Zhang", "a b", "a/b", "foo\n"} {
		if _, err := Decide(Input{Username: name}); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}

func TestSafeErrorRedacts(t *testing.T) {
	err := SafeError(errString("password=s3cret token=mul_abc key=sk-model"), "s3cret", "mul_abc", "sk-model")
	if err == nil || strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "mul_abc") || strings.Contains(err.Error(), "sk-model") {
		t.Fatal(err)
	}
}

func TestSafeErrorShortSecretDoesNotLoop(t *testing.T) {
	done := make(chan struct{})
	go func() {
		err := SafeError(errString("pw=ab tail"), "ab", "a")
		if err == nil || strings.Contains(err.Error(), "pw=ab") {
			t.Errorf("short secret leaked: %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SafeError looped")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

package agentconfig

import "testing"

func TestValidateMaxConcurrentTasks(t *testing.T) {
	// 0 = unlimited, 1–50 = cap.
	for _, value := range []int32{0, 1, 50} {
		if err := ValidateMaxConcurrentTasks(value); err != nil {
			t.Errorf("ValidateMaxConcurrentTasks(%d): %v", value, err)
		}
	}
	// Every negative value is rejected, including the legacy -1 sentinel,
	// and 51 stays out of range.
	for _, value := range []int32{-1, -2, 51} {
		if err := ValidateMaxConcurrentTasks(value); err == nil {
			t.Errorf("ValidateMaxConcurrentTasks(%d) succeeded", value)
		}
	}
}

func TestIsUnlimitedMaxConcurrentTasks(t *testing.T) {
	for _, value := range []int32{0, -1} {
		if !IsUnlimitedMaxConcurrentTasks(value) {
			t.Errorf("IsUnlimitedMaxConcurrentTasks(%d) = false, want true", value)
		}
	}
	for _, value := range []int32{1, 50} {
		if IsUnlimitedMaxConcurrentTasks(value) {
			t.Errorf("IsUnlimitedMaxConcurrentTasks(%d) = true, want false", value)
		}
	}
}

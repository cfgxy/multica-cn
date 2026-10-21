package agentconfig

import "testing"

func TestValidateMaxConcurrentTasks(t *testing.T) {
	for _, value := range []int32{-1, 1, 50} {
		if err := ValidateMaxConcurrentTasks(value); err != nil {
			t.Errorf("ValidateMaxConcurrentTasks(%d): %v", value, err)
		}
	}
	for _, value := range []int32{-2, 0, 51} {
		if err := ValidateMaxConcurrentTasks(value); err == nil {
			t.Errorf("ValidateMaxConcurrentTasks(%d) succeeded", value)
		}
	}
}

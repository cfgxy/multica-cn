package agentconfig

import "fmt"

const (
	DefaultMaxConcurrentTasks int32 = 6
	MinMaxConcurrentTasks     int32 = 1
	MaxMaxConcurrentTasks     int32 = 50
)

func IsValidMaxConcurrentTasks(value int32) bool {
	return value >= MinMaxConcurrentTasks && value <= MaxMaxConcurrentTasks
}

func ValidateMaxConcurrentTasks(value int32) error {
	if !IsValidMaxConcurrentTasks(value) {
		return fmt.Errorf(
			"must be between %d and %d",
			MinMaxConcurrentTasks,
			MaxMaxConcurrentTasks,
		)
	}
	return nil
}

// ResourceWeight (RUYI-397) scales how much of the claim budget one running
// task occupies: a task from a weight-3 agent counts as 3 slots against
// max_concurrent_tasks, so heavy workloads hit the same ceiling sooner than
// light ones instead of throttling purely by run count.
const (
	DefaultResourceWeight int32 = 1
	MinResourceWeight     int32 = 1
	MaxResourceWeight     int32 = 10
)

func ValidateResourceWeight(value int32) error {
	if value < MinResourceWeight || value > MaxResourceWeight {
		return fmt.Errorf(
			"must be between %d and %d",
			MinResourceWeight,
			MaxResourceWeight,
		)
	}
	return nil
}

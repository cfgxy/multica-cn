package execenv

import "testing"

func TestSnapshotGitEnvironmentBlocksConfigurationOverrides(t *testing.T) {
	env := snapshotGitEnvironment([]string{
		"PATH=/bin",
		"GIT_CONFIG_GLOBAL=/host/global",
		"GIT_CONFIG_SYSTEM=/host/system",
		"GIT_CONFIG_NOSYSTEM=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0=/host/hooks",
		"GIT_CONFIG_PARAMETERS='core.hooksPath=/host/hooks'",
	})
	values := make(map[string]string, len(env))
	for _, item := range env {
		for i := range item {
			if item[i] == '=' {
				values[item[:i]] = item[i+1:]
				break
			}
		}
	}
	if values["PATH"] != "/bin" {
		t.Fatalf("ordinary environment value = %q, want /bin", values["PATH"])
	}
	if values["GIT_CONFIG_NOSYSTEM"] != "1" || values["GIT_CONFIG_GLOBAL"] != "/dev/null" {
		t.Fatalf("snapshot config isolation = %#v", values)
	}
	for _, key := range []string{"GIT_CONFIG_SYSTEM", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_PARAMETERS"} {
		if _, ok := values[key]; ok {
			t.Fatalf("snapshot environment retained %s", key)
		}
	}
}

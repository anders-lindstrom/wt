package config

import "testing"

// timemachine is on by default on macOS and off everywhere else, and off
// macOS no file switches it on.
func TestTimeMachineOnByPlatform(t *testing.T) {
	cases := []struct {
		name, body, goos string
		want             bool
	}{
		{"macOS, no setting", "", "darwin", true},
		{"macOS, switched off", "timemachine = false\n", "darwin", false},
		{"macOS, switched on", "timemachine = true\n", "darwin", true},
		{"linux, no setting", "", "linux", false},
		{"linux, switched on", "timemachine = true\n", "linux", false},
		{"macOS, unreadable file", "timemachine = \n", "darwin", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, _ := loadUser(writeUser(t, c.body))
			if got := u.TimeMachineOn(c.goos); got != c.want {
				t.Errorf("TimeMachineOn(%q) = %v, want %v", c.goos, got, c.want)
			}
		})
	}
}

// The default `wt config` shows is this machine's: true on a Mac, false
// elsewhere, as the file would spell it.
func TestTimeMachineDefaultFollowsThePlatform(t *testing.T) {
	old := platform
	t.Cleanup(func() { platform = old })
	for goos, want := range map[string]string{"darwin": "true", "linux": "false", "windows": "false"} {
		platform = goos
		got, err := DefaultUser().Value(UserKeyTimeMachine)
		if err != nil || got != want {
			t.Errorf("on %s the default is %q (%v), want %q", goos, got, err, want)
		}
	}
}

// timemachine is a boolean like the other integrations: written by
// `wt config set`, read back, and refused when it is not true or false.
func TestTimeMachineIsSetLikeAnIntegration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, _, err := SetUser(UserKeyTimeMachine, "sometimes"); err == nil {
		t.Fatal("timemachine=sometimes was written")
	}
	if _, set, err := SetUser(UserKeyTimeMachine, "false"); err != nil || set != "false" {
		t.Fatalf("SetUser = %q, %v", set, err)
	}
	u, err := LoadUser()
	if err != nil {
		t.Fatal(err)
	}
	if u.TimeMachine || !u.IsSet(UserKeyTimeMachine) || u.Origin(UserKeyTimeMachine) != "user file" {
		t.Errorf("read back timemachine = %v, set = %v, origin %q",
			u.TimeMachine, u.IsSet(UserKeyTimeMachine), u.Origin(UserKeyTimeMachine))
	}
	if u.TimeMachineOn("darwin") {
		t.Error("switched off in the file, and still on for macOS")
	}
}

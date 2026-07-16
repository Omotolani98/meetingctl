package cli

import "testing"

func TestUpdatePackages(t *testing.T) {
	got := updatePackages()
	want := []string{
		"github.com/Omotolani98/meetingctl/cmd/meetingctl",
		"github.com/Omotolani98/meetingctl/cmd/meetingd",
		"github.com/Omotolani98/meetingctl/cmd/meeting-mcp",
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestInstallDir(t *testing.T) {
	old := getExecutable
	t.Cleanup(func() { getExecutable = old })
	getExecutable = func() (string, error) { return "/usr/local/bin/meetingctl", nil }

	got, err := installDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/local/bin" {
		t.Fatalf("got %q", got)
	}
}

func TestUpdateVersionLabel(t *testing.T) {
	if got := updateVersionLabel(" "); got != "latest" {
		t.Fatalf("got %q", got)
	}
	if got := updateVersionLabel("v1.2.3"); got != "v1.2.3" {
		t.Fatalf("got %q", got)
	}
}

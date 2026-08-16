package obsws

import (
	"os"
	"sort"
	"testing"

	"github.com/andreykaipov/goobs"
	obsconfig "github.com/andreykaipov/goobs/api/requests/config"
	"github.com/andreykaipov/goobs/api/requests/outputs"
)

/*
Reads the recording output configuration out of a *running* OBS.

OBS only flushes basic.ini and global.ini on certain events, so the files on
disk can lag well behind what the UI shows. This asks the live instance, which
is the only authoritative answer when the preview is black.

	OBSDECK_OBS=localhost:4455 go test ./internal/obsws/ -run TestDoctor -v
*/
func TestDoctorRecordingOutput(t *testing.T) {
	host := os.Getenv("OBSDECK_OBS")
	if host == "" {
		t.Skip("set OBSDECK_OBS=host:port to inspect a live OBS")
	}

	client, err := goobs.New(host, goobs.WithPassword(os.Getenv("OBS_PASSWORD")))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	if pl, err := client.Config.GetProfileList(); err == nil {
		t.Logf("active profile: %q  (all: %v)", pl.CurrentProfileName, pl.Profiles)
	}

	// These are the Advanced > Recording > Custom Output (FFmpeg) fields.
	for _, key := range []string{
		"RecType", "FFOutputToFile", "FFURL", "FFFilePath", "FFFormat",
		"FFVEncoder", "FFAEncoder", "FFVBitrate", "FFABitrate", "FFVGOPSize",
	} {
		p, err := client.Config.GetProfileParameter(
			obsconfig.NewGetProfileParameterParams().
				WithParameterCategory("AdvOut").WithParameterName(key))
		if err != nil {
			t.Logf("  AdvOut.%-16s <error: %v>", key, err)
			continue
		}
		val := p.ParameterValue
		if val == "" {
			val = "(empty)  default=" + p.DefaultParameterValue
		}
		t.Logf("  AdvOut.%-16s %s", key, val)
	}

	// Dumping the real settings map avoids guessing at config key names.
	ol, err := client.Outputs.GetOutputList()
	if err != nil {
		t.Fatalf("GetOutputList: %v", err)
	}
	for _, o := range ol.Outputs {
		t.Logf("output %-26s active=%-5v kind=%s", o.Name, o.Active, o.Kind)
		st, err := client.Outputs.GetOutputSettings(
			outputs.NewGetOutputSettingsParams().WithOutputName(o.Name))
		if err != nil {
			t.Logf("    <settings error: %v>", err)
			continue
		}
		keys := make([]string, 0, len(st.OutputSettings))
		for k := range st.OutputSettings {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.Logf("    %-22s %v", k, st.OutputSettings[k])
		}
	}
}

package media

import (
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRejectUntrustedLaunchTargets(t *testing.T) {
	registry := &PlayerRegistry{}
	for _, target := range []string{
		"", "--script=/tmp/evil", "-https://example.test", " https://example.test",
		"file:///tmp/evil.desktop", "javascript:alert(1)", "data:text/html,x",
		"mailto:x@example.test", "smb://example.test/share", "/tmp/file.pdf",
		"https:///missing-host", "https://example.test\n--option", "https://example.test/\x1b[2J",
		"https://example.test/\u009d52;c;x", "//example.test/path",
	} {
		t.Run(fmt.Sprintf("%q", target), func(t *testing.T) {
			require.Error(t, validateURL(target))
			// Must reject before consulting detector or starting any process.
			require.Error(t, (&Launcher{}).Open(target))
			_, err := registry.GetCommand("unused", TypeUnknown, target)
			require.Error(t, err)
		})
	}
	for _, target := range []string{"https://example.test", "http://example.test/video.mp4?q=a&b=c", "HTTPS://example.test/é"} {
		require.NoError(t, validateURL(target))
	}
}

func TestWindowsStartUsesExecutableWithoutShell(t *testing.T) {
	target := "https://example.test/a?x=1&calc.exe"
	cmd := playerCommand("start", []string{target}, "windows")
	require.Equal(t, []string{"rundll32.exe", "url.dll,FileProtocolHandler", target}, cmd.Args)
}

func TestGetCommandCopiesArgumentsDuringOverlappingCalls(t *testing.T) {
	backing := []string{"--quiet", "untouched", "untouched"}
	registry := &PlayerRegistry{players: map[string]PlayerDefinition{
		"test-player": {
			Platforms: []string{runtime.GOOS},
			Video:     &PlayerMediaTypeConfig{Args: backing[:1]},
		},
	}}
	const n = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	errors := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			target := fmt.Sprintf("https://example.test/%d", i)
			cmd, err := registry.GetCommand("test-player", TypeVideo, target)
			if err == nil && (len(cmd.Args) != 3 || cmd.Args[1] != "--quiet" || cmd.Args[2] != target) {
				err = fmt.Errorf("corrupt arguments: %q", cmd.Args)
			}
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, []string{"--quiet", "untouched", "untouched"}, backing)
}

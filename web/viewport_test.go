// Package web_test measures the built UI in a real browser at every viewport
// ADR-0025 names, so "works on a phone" is a check rather than a habit.
package web_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"
)

// ADR-0025's matrix. Touch says whether the 44 pixel minimum is asked for:
// 1024 by 768 is a tablet in landscape as often as it is a small laptop, and
// the wider of the two is where a mouse can finally be assumed.
var matrix = []struct {
	name          string
	width, height int
	touch         bool
}{
	{"320x568", 320, 568, true},
	{"360x800", 360, 800, true},
	{"375x812", 375, 812, true},
	{"390x844", 390, 844, true},
	{"430x932", 430, 932, true},
	{"600x960", 600, 960, true},
	{"767x1024", 767, 1024, true},
	{"768x1024", 768, 1024, true},
	{"769x1024", 769, 1024, true},
	{"1024x768", 1024, 768, true},
	{"1280x800", 1280, 800, false},
	{"1440x900", 1440, 900, false},
	{"1920x1080", 1920, 1080, false},
}

// Every route, plus the one panel that opens on a tap. A menu anchored to the
// right edge is the overflow nobody sees until they are holding a phone.
func routes() []route {
	return []route{
		{Name: "landing", Path: "/", Rendered: ".kinds .chip"},
		{Name: "landing, menu open", Path: "/", Rendered: ".kinds .chip", Click: ".menu-button"},
		{Name: "entity", Path: "/entity/" + url.PathEscape(fixtureRef), Rendered: ".identity"},
		{Name: "plugins", Path: "/plugins", Rendered: ".row.plugin"},
	}
}

// ADR-0025: no horizontal page scroll, ever. Wide content scrolls inside its
// own container, never the body.
func TestADR0025_NoViewportScrollsThePageSideways(t *testing.T) {
	for _, viewport := range matrix {
		t.Run(viewport.name, func(t *testing.T) {
			for _, measured := range render(t, viewport.width, viewport.height, viewport.touch) {
				if measured.DocScrollWidth > measured.ClientWidth {
					t.Errorf("%s scrolls sideways: the document is %dpx wide in a %dpx viewport.\n%s",
						measured.Page, measured.DocScrollWidth, measured.ClientWidth, blame(measured))
				}
				if measured.BodyScrollWidth > measured.ClientWidth {
					t.Errorf("%s scrolls sideways: the body is %dpx wide in a %dpx viewport.\n%s",
						measured.Page, measured.BodyScrollWidth, measured.ClientWidth, blame(measured))
				}
			}
		})
	}
}

// ADR-0025: interactive targets at least 44 by 44 pixels on touch viewports.
func TestADR0025_EveryTouchTargetIsBigEnoughToHit(t *testing.T) {
	for _, viewport := range matrix {
		if !viewport.touch {
			continue
		}

		t.Run(viewport.name, func(t *testing.T) {
			for _, measured := range render(t, viewport.width, viewport.height, viewport.touch) {
				// A page whose controls the selector never matched passes both
				// checks below while testing neither.
				if measured.Counted == 0 {
					t.Fatalf("%s: nothing interactive was measured, so this asserts nothing", measured.Page)
				}
				for _, small := range measured.Small {
					t.Errorf("%s: %s is %.0f by %.0f, under the 44 pixel minimum",
						measured.Page, small.Selector, small.Width, small.Height)
				}
			}
		})
	}
}

func blame(measured measurement) string {
	if len(measured.Overflowing) == 0 {
		return "Nothing sticks out on its own, so this is a track that grew rather than one element."
	}
	out := "Sticking out, with no scroller of its own:"
	for _, culprit := range measured.Overflowing {
		out += "\n  " + culprit
	}
	return out
}

// render measures every route at one viewport. What the page says it laid out
// at is confirmed before anything else is believed: a browser that quietly went
// wider reports overflow that is not there.
func render(t *testing.T, width, height int, touch bool) []measurement {
	t.Helper()

	measured := drive(t, width, height, touch)
	for _, one := range measured {
		if one.Problem != "" {
			t.Fatalf("the harness gave up on %s: %s", one.Page, one.Problem)
		}
		if one.InnerWidth != width || one.ClientWidth != width {
			t.Fatalf("%s laid out at %dpx (client %dpx) after being asked for %dpx, so nothing measured here is about %dpx",
				one.Page, one.InnerWidth, one.ClientWidth, width, width)
		}
		if one.Coarse != touch {
			t.Fatalf("%s reports pointer: coarse = %v, want %v: the emulation did not take, so the touch rules were never applied",
				one.Page, one.Coarse, touch)
		}
	}
	if len(measured) != len(routes()) {
		t.Fatalf("measured %d routes, want %d", len(measured), len(routes()))
	}
	return measured
}

func drive(t *testing.T, width, height int, touch bool) []measurement {
	t.Helper()

	chrome := browser(t)
	root, ok := bundled()
	if !ok {
		absent(t, "this checkout has no built UI: run `make web`")
	}

	shell, err := shellOf(root)
	if err != nil {
		t.Fatalf("read the app shell: %v", err)
	}

	posted := make(chan []measurement, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+harnessPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(harness(width, height, touch, routes()))
	})
	mux.HandleFunc("POST "+reportPath, func(w http.ResponseWriter, r *http.Request) {
		var body []measurement
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("the harness posted something unreadable: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
		select {
		case posted <- body:
		default:
		}
	})
	mux.Handle("/", fixtureHandler(shell, http.FileServer(root), root))

	server := httptest.NewServer(mux)
	defer server.Close()

	// Not t.TempDir: killing Chrome leaves its helper processes writing into the
	// profile for a moment, and the failed cleanup is reported as a test error.
	profile, err := os.MkdirTemp("", "dusk-viewport")
	if err != nil {
		t.Fatalf("make a browser profile: %v", err)
	}
	defer func() { _ = os.RemoveAll(profile) }()

	cmd := exec.Command(chrome, chromeArgs(profile, server.URL+harnessPath, touch)...) //nolint:gosec // the path is a browser this test located
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", chrome, err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	select {
	case measured := <-posted:
		return measured
	case <-time.After(90 * time.Second):
		t.Fatalf("%s never reported at %dx%d", chrome, width, height)
		return nil
	}
}

// chromeArgs runs headless with no window and no network of its own. What it
// asks for is confirmed in the page rather than trusted.
func chromeArgs(profile, target string, touch bool) []string {
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-background-networking",
		"--disable-sync",
		"--mute-audio",

		// A phone has overlay scrollbars, so hiding them is the faithful
		// layout as well as the one that measures the same on every platform.
		"--hide-scrollbars",

		// Animations that move an element make a measurement depend on when it
		// was taken. Every one of them is already inside a reduced-motion query.
		"--force-prefers-reduced-motion",

		"--user-data-dir=" + profile,

		// Wide enough that the frame is never the thing being clamped. The
		// frame carries the viewport; this window only has to contain it.
		"--window-size=1800,1200",
	}

	// A coarse pointer with no hover is what the CSS branches on, and headless
	// is a mouse until it is told otherwise. 2 is coarse, 1 is none.
	if touch {
		args = append(args, "--blink-settings=primaryPointerType=2,availablePointerTypes=2,primaryHoverType=1,availableHoverTypes=1")
	}

	return append(args, target)
}

func shellOf(root http.FileSystem) ([]byte, error) {
	file, err := root.Open("/index.html")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

// browser locates a Chrome. DUSK_TEST_CHROME names one that is somewhere else.
func browser(t *testing.T) string {
	t.Helper()

	if named := os.Getenv("DUSK_TEST_CHROME"); named != "" {
		return named
	}
	for _, candidate := range []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if found, err := exec.LookPath(candidate); err == nil {
			return found
		}
	}

	absent(t, "no Chrome found: install one, or name it in DUSK_TEST_CHROME")
	return ""
}

// absent skips, unless the runner has said that a skip is a failure. The
// matrix existed as a habit for one release; a suite that silently stops
// running it is the same thing with a green tick on it.
func absent(t *testing.T, why string) {
	t.Helper()

	if os.Getenv("DUSK_TEST_NO_SKIP") != "" {
		t.Fatal(why)
	}
	t.Skip(why)
}

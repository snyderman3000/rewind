package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const version = "0.6.0"
const userAgent = "Rewind/" + version + " (OnionOS movie guide)"

// Config lives in App/Rewind/data/config.json.
type Config struct {
	TMDBKey      string `json:"tmdb_api_key"`  // TMDB v3 API key or v4 read access token
	OMDbKey      string `json:"omdb_api_key"`  // OMDb key, for Rotten Tomatoes / IMDb / Metacritic scores
	Region       string `json:"region"`        // country for streaming services and age ratings
	Language     string `json:"language"`      // TMDB language for titles and synopses
	Days         int    `json:"days"`          // how far back a digital release still counts as new
	MaxMovies    int    `json:"max_movies"`    // how many titles to keep in the guide
	Providers    []int  `json:"providers"`     // optional TMDB provider ids to limit to (8 = Netflix…)
	RefreshHours int    `json:"refresh_hours"` // how old the guide can get before it refreshes on launch

	// Radarr on the home network (optional)
	RadarrURL     string `json:"radarr_url"`                  // e.g. http://192.168.1.20:7878
	RadarrKey     string `json:"radarr_api_key"`              // Radarr → Settings → General → API Key
	RadarrProfile string `json:"radarr_quality_profile"`      // profile name or id; blank = Radarr's first
	RadarrRoot    string `json:"radarr_root_folder"`          // root folder path; blank = Radarr's first
	RadarrSearch  *bool  `json:"radarr_search,omitempty"`     // search as soon as it's added (default true)
	RadarrAvail   string `json:"radarr_minimum_availability"` // announced | inCinemas | released (default)

	Problem string `json:"-"` // set when config.json couldn't be read, for the UI to show
}

func defaultConfig() Config {
	return Config{Region: "US", Language: "en-US", Days: 120, MaxMovies: 250, RefreshHours: 6, Providers: []int{}}
}

type Env struct {
	AppDir  string
	DataDir string
	Config  Config
	HTTP    *http.Client
	// overridable for tests
	TMDBBase  string
	OMDbBase  string
	ImageBase string
	Now       func() time.Time
}

func newEnv() *Env {
	exe, _ := os.Executable()
	appDir := filepath.Dir(exe)
	env := &Env{AppDir: appDir, DataDir: filepath.Join(appDir, "data"), Now: time.Now}
	if d := os.Getenv("REWIND_DATA"); d != "" {
		env.DataDir = d
	}
	os.MkdirAll(env.DataDir, 0o755)
	env.Config = loadConfig(filepath.Join(env.DataDir, "config.json"))
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 8
	tr.MaxIdleConnsPerHost = 4
	tr.ResponseHeaderTimeout = 25 * time.Second
	env.HTTP = &http.Client{Transport: tr, Timeout: 40 * time.Second}
	return env
}

// loadConfig reads config.json, filling in defaults, and writes a starter file if there is none.
func loadConfig(path string) Config {
	cfg := defaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		b, _ := json.MarshalIndent(cfg, "", "  ")
		os.WriteFile(path, b, 0o644)
		return cfg
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		log.Printf("config.json is not valid JSON: %v", err)
		cfg = defaultConfig()
		cfg.Problem = configProblem(b, err)
	}
	d := defaultConfig()
	cfg.TMDBKey = strings.TrimSpace(cfg.TMDBKey)
	cfg.OMDbKey = strings.TrimSpace(cfg.OMDbKey)
	cfg.RadarrURL = strings.TrimSpace(cfg.RadarrURL)
	cfg.RadarrKey = strings.TrimSpace(cfg.RadarrKey)
	if cfg.Region == "" {
		cfg.Region = d.Region
	}
	cfg.Region = strings.ToUpper(cfg.Region)
	if cfg.Language == "" {
		cfg.Language = d.Language
	}
	if cfg.Days <= 0 {
		cfg.Days = d.Days
	}
	if cfg.MaxMovies <= 0 {
		cfg.MaxMovies = d.MaxMovies
	}
	if cfg.MaxMovies > 400 {
		cfg.MaxMovies = 400
	}
	if cfg.RefreshHours <= 0 {
		cfg.RefreshHours = d.RefreshHours
	}
	return cfg
}

func main() {
	shot := flag.String("shot", "", "render a scene to PNG instead of running on the device (boot|list|filter|detail|trailer|keyboard|search|setup|bye)")
	out := flag.String("out", "shot.png", "PNG path for -shot")
	w := flag.Int("w", 640, "screen width for -shot")
	h := flag.Int("h", 480, "screen height for -shot")
	bench := flag.Int("bench", 0, "with -shot: render the scene N times and print the average frame time")
	icon := flag.String("icon", "", "write the 74×74 OnionOS app icon to this PNG path and exit")
	flag.Parse()
	if *icon != "" {
		f, err := os.Create(*icon)
		if err != nil {
			log.Fatal(err)
		}
		png.Encode(f, appIcon(74))
		f.Close()
		return
	}

	env := newEnv()
	if *shot != "" {
		c := NewCanvas(*w, *h)
		ui := NewUI(env, c.W, c.H)
		demoScene(ui, *shot)
		ui.Draw(c)
		if *bench > 0 {
			t0 := time.Now()
			for i := 0; i < *bench; i++ {
				ui.frame++
				ui.Draw(c)
			}
			fmt.Printf("%s: %.1f ms/frame\n", *shot, float64(time.Since(t0).Microseconds())/1000/float64(*bench))
		}
		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		png.Encode(f, c)
		f.Close()
		return
	}

	logPath := filepath.Join(env.DataDir, "rewind.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 512<<10 {
		os.Rename(logPath, logPath+".1")
	}
	logf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if logf != nil {
		log.SetOutput(logf)
	}
	log.Printf("rewind %s starting, region=%s", version, env.Config.Region)

	fb, err := OpenFramebuffer()
	if err != nil {
		log.Fatalf("framebuffer: %v", err)
	}
	defer fb.Close()
	c := NewCanvas(fb.W, fb.H)
	keys := make(chan int, 32)
	if err := ReadInput("/dev/input/event0", keys); err != nil {
		log.Fatalf("input: %v", err)
	}
	ui := NewUI(env, c.W, c.H)
	ui.Start()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	lastMinute := -1
	dirty := true
	for !ui.quit {
		select {
		case k := <-keys:
			ui.Key(k)
			dirty = true
		case f := <-ui.post:
			f()
			dirty = true
		case <-tick.C:
			ui.frame++
			ui.Tick()
			if ui.Animating() {
				dirty = true
			}
			if m := time.Now().Minute(); m != lastMinute {
				lastMinute = m
				dirty = true
			}
		}
		if dirty {
			ui.Draw(c)
			fb.Present(c)
			dirty = false
		}
	}
	ui.saveState()
	// "Be kind, rewind" sign-off
	ui.scr = scrBye
	for i := 0; i < 14; i++ {
		ui.frame++
		ui.Draw(c)
		fb.Present(c)
		time.Sleep(50 * time.Millisecond)
	}
	fb.Clear()
}

// configProblem explains a broken config.json in terms of lines a person can find.
func configProblem(b []byte, err error) string {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se):
		off := int(se.Offset)
		if off > len(b) {
			off = len(b)
		}
		line := 1 + bytes.Count(b[:max(0, off-1)], []byte("\n"))
		msg := se.Error()
		if strings.Contains(msg, "after object key:value pair") {
			// the usual slip: a missing comma at the end of the previous entry
			prev := line - 1
			for prev > 1 && strings.TrimSpace(lineOf(b, prev)) == "" {
				prev--
			}
			return fmt.Sprintf("config.json has a missing comma at the end of line %d:\n%s\n\nAdd a comma there, save, and open Rewind again. Until then none of your settings are used.", prev, strings.TrimSpace(lineOf(b, prev)))
		}
		return fmt.Sprintf("config.json has a mistake near line %d (%s):\n%s\n\nFix it, save, and open Rewind again. Until then none of your settings are used.", line, msg, strings.TrimSpace(lineOf(b, line)))
	case errors.As(err, &te):
		return fmt.Sprintf("In config.json, %q should be a %s, not a %s (for example, numbers without quotes).", te.Field, te.Type, te.Value)
	}
	return "config.json couldn't be read: " + err.Error()
}

func lineOf(b []byte, n int) string {
	lines := strings.Split(string(b), "\n")
	if n < 1 || n > len(lines) {
		return ""
	}
	return lines[n-1]
}

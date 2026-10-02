package main

// Radarr (v3 API) on the home network: see which guide movies are already in
// the library, and add one so Radarr grabs it and it lands in Plex.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RadarrMovie is what Rewind needs to know about a movie already in Radarr.
type RadarrMovie struct {
	ID        int  `json:"id"`
	TMDBID    int  `json:"tmdbId"`
	HasFile   bool `json:"hasFile"`
	Monitored bool `json:"monitored"`
}

type RadarrProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type RadarrFolder struct {
	ID         int    `json:"id"`
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	FreeSpace  int64  `json:"freeSpace"`
}

// RadarrTarget is where a new movie goes: the quality profile and root folder.
type RadarrTarget struct {
	Profile RadarrProfile
	Folder  RadarrFolder
	Search  bool
}

// RadarrQueue is a movie's download in progress.
type RadarrQueue struct {
	Size     float64 `json:"size"`
	SizeLeft float64 `json:"sizeleft"`
	Status   string  `json:"status"`
	State    string  `json:"trackedDownloadState"`
	TimeLeft string  `json:"timeleft"`
}

func (q *RadarrQueue) Percent() int {
	if q.Size <= 0 {
		return 0
	}
	return int((q.Size - q.SizeLeft) * 100 / q.Size)
}

func (env *Env) RadarrOn() bool {
	return env.Config.RadarrURL != "" && env.Config.RadarrKey != ""
}

func (env *Env) radarrBase() string {
	u := strings.TrimRight(strings.TrimSpace(env.Config.RadarrURL), "/")
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	return u + "/api/v3"
}

func (env *Env) radarrDo(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, env.radarrBase()+path, rd)
	if err != nil {
		return &APIError{"Radarr", 0, "radarr_url in config.json isn't a valid address"}
	}
	req.Header.Set("X-Api-Key", env.Config.RadarrKey)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cl := &http.Client{Timeout: 20 * time.Second, Transport: env.HTTP.Transport}
	resp, err := cl.Do(req)
	if err != nil {
		return radarrNetErr(env.Config.RadarrURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return &APIError{"Radarr", 401, "Radarr rejected the API key — copy it from Radarr's Settings → General into radarr_api_key"}
	case resp.StatusCode == 404 && strings.HasPrefix(path, "/movie/lookup"):
		return &APIError{"Radarr", 404, "Radarr couldn't find this movie"}
	case resp.StatusCode == 404:
		return &APIError{"Radarr", 404, "that address answered, but it isn't Radarr's API — check radarr_url (include any URL base, like /radarr)"}
	case resp.StatusCode >= 400:
		return &APIError{"Radarr", resp.StatusCode, "Radarr: " + radarrMessage(data, resp.Status)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &APIError{"Radarr", resp.StatusCode, "that address didn't answer like Radarr — check radarr_url"}
	}
	return nil
}

// radarrMessage pulls the readable part out of Radarr's validation errors.
func radarrMessage(data []byte, status string) string {
	var list []struct {
		Msg string `json:"errorMessage"`
	}
	if json.Unmarshal(data, &list) == nil && len(list) > 0 && list[0].Msg != "" {
		return list[0].Msg
	}
	var one struct {
		Msg string `json:"message"`
	}
	if json.Unmarshal(data, &one) == nil && one.Msg != "" {
		return one.Msg
	}
	return status
}

func radarrNetErr(addr string, err error) error {
	s := err.Error()
	if errors.Is(err, context.Canceled) {
		return err
	}
	switch {
	case strings.Contains(s, "refused"):
		return &APIError{"Radarr", 0, "nothing answered at " + addr + " — is Radarr running?"}
	case strings.Contains(s, "no route") || strings.Contains(s, "unreachable") || strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return &APIError{"Radarr", 0, "can't reach Radarr at " + addr + " — is the Pi on, and is the Miyoo on your home Wi-Fi?"}
	case strings.Contains(s, "no such host"):
		return &APIError{"Radarr", 0, "can't find " + addr + " — try the Pi's IP address in radarr_url"}
	}
	return err
}

// RadarrLibrary returns every movie in Radarr, keyed by TMDB id.
func (env *Env) RadarrLibrary(ctx context.Context) (map[int]RadarrMovie, error) {
	var ms []RadarrMovie
	if err := env.radarrDo(ctx, "GET", "/movie", nil, &ms); err != nil {
		return nil, err
	}
	out := make(map[int]RadarrMovie, len(ms))
	for _, m := range ms {
		if m.TMDBID > 0 {
			out[m.TMDBID] = m
		}
	}
	return out, nil
}

// RadarrTarget picks the quality profile and root folder named in config, or the first ones.
func (env *Env) RadarrTarget(ctx context.Context) (*RadarrTarget, error) {
	var profiles []RadarrProfile
	if err := env.radarrDo(ctx, "GET", "/qualityprofile", nil, &profiles); err != nil {
		return nil, err
	}
	var folders []RadarrFolder
	if err := env.radarrDo(ctx, "GET", "/rootfolder", nil, &folders); err != nil {
		return nil, err
	}
	t := &RadarrTarget{Search: env.Config.RadarrSearch == nil || *env.Config.RadarrSearch}
	want := strings.TrimSpace(env.Config.RadarrProfile)
	for _, p := range profiles {
		if want == "" || strings.EqualFold(p.Name, want) || strconv.Itoa(p.ID) == want {
			t.Profile = p
			break
		}
	}
	if t.Profile.ID == 0 {
		if len(profiles) == 0 {
			return nil, &APIError{"Radarr", 0, "Radarr has no quality profiles set up"}
		}
		var names []string
		for _, p := range profiles {
			names = append(names, p.Name)
		}
		return nil, &APIError{"Radarr", 0, fmt.Sprintf("Radarr has no quality profile called %q — it has: %s", want, strings.Join(names, ", "))}
	}
	wantDir := strings.TrimRight(strings.TrimSpace(env.Config.RadarrRoot), "/")
	for _, f := range folders {
		if wantDir == "" && f.Accessible || wantDir != "" && strings.TrimRight(f.Path, "/") == wantDir {
			t.Folder = f
			break
		}
	}
	if t.Folder.Path == "" {
		if wantDir != "" {
			return nil, &APIError{"Radarr", 0, "Radarr has no root folder " + wantDir + " — add it in Radarr's Settings → Media Management"}
		}
		return nil, &APIError{"Radarr", 0, "Radarr has no usable root folder — add one in Settings → Media Management"}
	}
	return t, nil
}

// RadarrAdd adds the movie to Radarr, monitored, and (by default) starts a search for it.
func (env *Env) RadarrAdd(ctx context.Context, tmdbID int, t *RadarrTarget) (RadarrMovie, error) {
	var lookup map[string]any
	if err := env.radarrDo(ctx, "GET", "/movie/lookup/tmdb?tmdbId="+strconv.Itoa(tmdbID), nil, &lookup); err != nil {
		return RadarrMovie{}, err
	}
	if id, _ := lookup["id"].(float64); id > 0 {
		// already in the library
		return RadarrMovie{ID: int(id), TMDBID: tmdbID, HasFile: lookup["hasFile"] == true, Monitored: lookup["monitored"] == true}, nil
	}
	avail := env.Config.RadarrAvail
	if avail == "" {
		avail = "released"
	}
	lookup["qualityProfileId"] = t.Profile.ID
	lookup["rootFolderPath"] = t.Folder.Path
	lookup["monitored"] = true
	lookup["minimumAvailability"] = avail
	lookup["addOptions"] = map[string]any{"searchForMovie": t.Search, "monitor": "movieOnly"}
	delete(lookup, "path") // let Radarr build the folder name under the root folder
	var added RadarrMovie
	if err := env.radarrDo(ctx, "POST", "/movie", lookup, &added); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && strings.Contains(strings.ToLower(ae.Msg), "already been added") {
			return RadarrMovie{TMDBID: tmdbID, Monitored: true}, nil
		}
		return RadarrMovie{}, err
	}
	if added.TMDBID == 0 {
		added.TMDBID = tmdbID
	}
	return added, nil
}

// RadarrQueueFor returns the active download for a Radarr movie id, or nil.
func (env *Env) RadarrQueueFor(ctx context.Context, radarrID int) (*RadarrQueue, error) {
	var qs []RadarrQueue
	q := url.Values{}
	q.Set("movieId", strconv.Itoa(radarrID))
	if err := env.radarrDo(ctx, "GET", "/queue/details?"+q.Encode(), nil, &qs); err != nil {
		return nil, err
	}
	if len(qs) == 0 {
		return nil, nil
	}
	return &qs[0], nil
}

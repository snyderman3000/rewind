package main

// OMDb supplies the Rotten Tomatoes Tomatometer, IMDb rating and Metascore
// (looked up by IMDb id). Free keys allow 1,000 lookups a day, so scores are cached.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (env *Env) omdbBase() string {
	if env.OMDbBase != "" {
		return env.OMDbBase
	}
	return "https://www.omdbapi.com/"
}

type omdbResp struct {
	Response   string `json:"Response"`
	Error      string `json:"Error"`
	Rated      string `json:"Rated"`
	Metascore  string `json:"Metascore"`
	IMDbRating string `json:"imdbRating"`
	IMDbVotes  string `json:"imdbVotes"`
	Awards     string `json:"Awards"`
	BoxOffice  string `json:"BoxOffice"`
	Ratings    []struct {
		Source string `json:"Source"`
		Value  string `json:"Value"`
	} `json:"Ratings"`
}

// Scores looks up m.IMDbID on OMDb and sets RT, Meta and IMDb. It's a no-op without a key or id.
func (env *Env) Scores(ctx context.Context, m *Movie) error {
	key := env.Config.OMDbKey
	if key == "" || m.IMDbID == "" {
		return nil
	}
	q := url.Values{}
	q.Set("i", m.IMDbID)
	q.Set("apikey", key)
	req, _ := http.NewRequestWithContext(ctx, "GET", env.omdbBase()+"?"+q.Encode(), nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := env.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var r omdbResp
	if err := json.Unmarshal(body, &r); err != nil {
		return &APIError{"OMDb", resp.StatusCode, "OMDb answered " + resp.Status}
	}
	if r.Response != "True" {
		e := strings.ToLower(r.Error)
		switch {
		case strings.Contains(e, "limit"):
			return &APIError{"OMDb", 429, "OMDb's daily limit is used up — scores refresh tomorrow"}
		case strings.Contains(e, "key"):
			return &APIError{"OMDb", 401, "OMDb rejected the key — check omdb_api_key in App/Rewind/data/config.json"}
		}
		// "Incorrect IMDb ID." / "Movie not found!": nothing to show, but not an error
		m.ScoresAt = env.Now()
		return nil
	}
	applyScores(m, &r)
	m.ScoresAt = env.Now()
	return nil
}

func applyScores(m *Movie, r *omdbResp) {
	m.RT, m.Meta, m.IMDb = -1, -1, 0
	for _, x := range r.Ratings {
		switch x.Source {
		case "Rotten Tomatoes":
			if v, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(x.Value), "%")); err == nil {
				m.RT = v
			}
		case "Metacritic":
			if v, err := strconv.Atoi(strings.SplitN(x.Value, "/", 2)[0]); err == nil {
				m.Meta = v
			}
		case "Internet Movie Database":
			if v, err := strconv.ParseFloat(strings.SplitN(x.Value, "/", 2)[0], 64); err == nil {
				m.IMDb = v
			}
		}
	}
	if m.Meta < 0 {
		if v, err := strconv.Atoi(r.Metascore); err == nil {
			m.Meta = v
		}
	}
	if m.IMDb == 0 {
		if v, err := strconv.ParseFloat(r.IMDbRating, 64); err == nil {
			m.IMDb = v
		}
	}
	m.IMDbVotes = r.IMDbVotes
	if a := strings.TrimSpace(r.Awards); a != "" && a != "N/A" {
		m.Awards = a
	} else {
		m.Awards = ""
	}
	if m.Cert == "" && r.Rated != "" && r.Rated != "N/A" {
		m.Cert = r.Rated
	}
}

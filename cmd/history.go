package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/bjarneo/cliamp/history"
)

// HistoryShow prints recently played tracks, newest first. limit <= 0 prints all.
// When jsonOutput is true, output is a JSON array suitable for scripting.
func HistoryShow(limit int, jsonOutput bool) error {
	store := history.New()
	if store == nil {
		return fmt.Errorf("could not resolve config directory")
	}

	entries, err := store.Recent(limit)
	if err != nil {
		return fmt.Errorf("read history: %w", err)
	}

	if jsonOutput {
		type jsonEntry struct {
			PlayedAt     string `json:"played_at"`
			Path         string `json:"path"`
			Title        string `json:"title"`
			Artist       string `json:"artist,omitempty"`
			Album        string `json:"album,omitempty"`
			Genre        string `json:"genre,omitempty"`
			Year         int    `json:"year,omitempty"`
			TrackNumber  int    `json:"track_number,omitempty"`
			DurationSecs int    `json:"duration_secs,omitempty"`
		}
		out := make([]jsonEntry, len(entries))
		for i, e := range entries {
			out[i] = jsonEntry{
				PlayedAt:     e.PlayedAt.UTC().Format(time.RFC3339),
				Path:         e.Track.Path,
				Title:        e.Track.Title,
				Artist:       e.Track.Artist,
				Album:        e.Track.Album,
				Genre:        e.Track.Genre,
				Year:         e.Track.Year,
				TrackNumber:  e.Track.TrackNumber,
				DurationSecs: e.Track.DurationSecs,
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(entries) == 0 {
		fmt.Println("No history yet — listen to a track for at least 50% of its duration to record it.")
		return nil
	}

	fmt.Printf("Recently Played (%d tracks)\n\n", len(entries))
	now := time.Now()
	for i, e := range entries {
		fmt.Printf("  %3d. %s  (%s)\n", i+1, e.Track.DisplayName(), formatRelative(now, e.PlayedAt))
	}
	return nil
}

// HistoryClear wipes the history file.
func HistoryClear() error {
	store := history.New()
	if store == nil {
		return fmt.Errorf("could not resolve config directory")
	}
	if err := store.Clear(); err != nil {
		return fmt.Errorf("clear history: %w", err)
	}
	fmt.Println("History cleared.")
	return nil
}

// formatRelative renders a short human-friendly duration like "3m ago" or
// "yesterday". Falls back to the date when older than a week.
func formatRelative(now, then time.Time) string {
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return then.Local().Format("2006-01-02")
}

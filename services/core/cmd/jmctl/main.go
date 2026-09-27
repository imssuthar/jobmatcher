// Command jmctl is a small CLI for the core API.
//
//	jmctl upload resume.pdf   upload, wait for processing, print profile + facts
//	jmctl profile             print the active profile
//	jmctl facts               list the active fact bank
//	jmctl search "query"      semantic search over facts
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var base = strings.TrimRight(envOr("CORE_URL", "http://localhost:8080"), "/")

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "upload":
		if len(os.Args) < 3 {
			usage()
		}
		err = upload(os.Args[2])
	case "profile":
		err = printProfile("/v1/profile")
	case "facts":
		err = printFacts("/v1/profile/facts")
	case "search":
		if len(os.Args) < 3 {
			usage()
		}
		err = search(strings.Join(os.Args[2:], " "))
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: jmctl upload <file> | profile | facts | search <query>")
	os.Exit(2)
}

func upload(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", filepath.Base(path))
	_, _ = part.Write(data)
	_ = w.Close()
	resp, err := http.Post(base+"/v1/resumes", w.FormDataContentType(), &body)
	if err != nil {
		return err
	}
	var r struct {
		ID, Status, Error string
		FailedStage       string `json:"failed_stage"`
		ProfileID         string `json:"profile_id"`
		Deduplicated      bool
	}
	if err := decode(resp, &r); err != nil {
		return err
	}
	if r.Deduplicated {
		fmt.Println("This file was already processed; showing the stored result.")
	}
	start := time.Now()
	last := ""
	for r.Status != "ready" && r.Status != "failed" {
		if r.Status != last {
			fmt.Printf("  %-20s %5.0fs\n", r.Status, time.Since(start).Seconds())
			last = r.Status
		}
		time.Sleep(2 * time.Second)
		resp, err := http.Get(base + "/v1/resumes/" + r.ID)
		if err != nil {
			return err
		}
		if err := decode(resp, &r); err != nil {
			return err
		}
	}
	if r.Status == "failed" {
		return fmt.Errorf("processing failed at %s: %s", r.FailedStage, r.Error)
	}
	fmt.Printf("  %-20s %5.0fs\n\n", "ready", time.Since(start).Seconds())
	// Show the profile built from this file, which is not necessarily the
	// active one when the upload was deduplicated.
	if err := printProfile("/v1/profiles/" + r.ProfileID); err != nil {
		return err
	}
	return printFacts("/v1/profiles/" + r.ProfileID + "/facts")
}

func printProfile(path string) error {
	var p struct {
		Version  int
		Model    string
		IsActive bool `json:"is_active"`
		Data    struct {
			Name, Email, Location, Headline string
			TotalYearsExperience            float64 `json:"total_years_experience"`
			Skills                          []string
			Experience                      []struct{ Company, Title, Start, End string }
		}
	}
	if err := get(path, &p); err != nil {
		return err
	}
	d := p.Data
	active := ""
	if !p.IsActive {
		active = ", not the active version"
	}
	fmt.Printf("PROFILE v%d (%s%s)\n", p.Version, p.Model, active)
	fmt.Printf("  %s · %s\n  %s · %s · %.0f years\n", d.Name, d.Headline, d.Email, d.Location, d.TotalYearsExperience)
	fmt.Printf("  Skills: %s\n", strings.Join(d.Skills, ", "))
	for _, e := range d.Experience {
		fmt.Printf("  - %s, %s (%s – %s)\n", e.Title, e.Company, e.Start, e.End)
	}
	fmt.Println()
	return nil
}

func printFacts(path string) error {
	var out struct {
		Count int
		Facts []struct{ Text, Category string }
	}
	if err := get(path, &out); err != nil {
		return err
	}
	fmt.Printf("FACT BANK (%d facts)\n", out.Count)
	for i, f := range out.Facts {
		fmt.Printf("  %2d. [%s] %s\n", i+1, f.Category, f.Text)
	}
	return nil
}

func search(q string) error {
	var out struct {
		Results []struct {
			Text  string
			Score float64
		}
	}
	if err := get("/v1/profile/facts/search?k=5&q="+url.QueryEscape(q), &out); err != nil {
		return err
	}
	for _, r := range out.Results {
		fmt.Printf("  %.3f  %s\n", r.Score, r.Text)
	}
	return nil
}

func get(path string, out any) error {
	resp, err := http.Get(base + path)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

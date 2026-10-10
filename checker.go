package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
	"golang.org/x/net/html"
)

type Checker struct {
	User      string
	Pass      string
	OutputDir string
	Debug     bool
	Logger    *log.Logger
}

func (c *Checker) Run() RunRecord {
	startTime := time.Now()
	record := RunRecord{
		Timestamp: startTime,
		Downloads: []string{},
	}

	// Setup context
	ctx, cancel := c.createContext()
	defer cancel()

	// Login
	if err := c.login(ctx); err != nil {
		c.Logger.Println("Login failed:", err)
		record.LogSummary = fmt.Sprintf("Login failed: %v", err)
		record.Duration = time.Since(startTime).String()
		return record
	}

	// Check activity
	matches, err := c.checkActivity(ctx)
	if err != nil {
		c.Logger.Println("Activity check failed:", err)
		record.LogSummary = fmt.Sprintf("Activity check failed: %v", err)
		record.Duration = time.Since(startTime).String()
		return record
	}
	record.ItemsFound = len(matches)

	// Download
	downloaded := c.processMatches(ctx, matches)
	record.Downloads = downloaded
	record.ItemsDownloaded = len(downloaded)

	record.LogSummary = "Success"
	record.Duration = time.Since(startTime).String()
	return record
}

func (c *Checker) createContext() (context.Context, context.CancelFunc) {
	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.NoSandbox,
	}
	if p := os.Getenv("CHROME_PATH"); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	// If you wanted to see the browser, you'd remove Headless. For now default is headless.

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx, chromedp.WithLogf(c.Logger.Printf))

	// Separate cancel function to handle both
	finalCancel := func() {
		cancelCtx()
		cancelAlloc()
	}

	// Set timeout
	ctx, cancelTimeout := context.WithTimeout(ctx, 600*time.Second)

	return ctx, func() {
		cancelTimeout()
		finalCancel()
	}
}

func (c *Checker) login(ctx context.Context) error {
	c.Logger.Println("Attempting to log in...")
	var body, url string
	err := chromedp.Run(ctx,
		chromedp.Navigate(loginUrl),
		chromedp.WaitReady(`#nev`, chromedp.ByID),
		chromedp.SendKeys(`#nev`, c.User, chromedp.ByID),
		chromedp.SendKeys(`[name="pass"]`, c.Pass, chromedp.ByQuery),
		chromedp.Click(`[type="submit"]`, chromedp.ByQuery),
		chromedp.Sleep(10*time.Second),
		chromedp.Location(&url),
		chromedp.OuterHTML(`html`, &body, chromedp.ByQuery),
	)
	if err != nil {
		return err
	}
	c.Logger.Println("After submit URL:", url)
	_ = os.WriteFile("login_debug.html", []byte(body), 0644)

	// nCore may redirect to a shop/ad page after login; go straight to hitnrun.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(activityUrl),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Location(&url),
		chromedp.OuterHTML(`html`, &body, chromedp.ByQuery),
	); err != nil {
		return err
	}
	if strings.Contains(url, "login.php") || !strings.Contains(body, c.User) {
		return fmt.Errorf("login failed, landed on %s (see login_debug.html)", url)
	}
	c.Logger.Println("Login successful.")
	return nil
}

func (c *Checker) checkActivity(ctx context.Context) ([]string, error) {
	c.Logger.Println("Opening activity page...")
	var body string
	err := chromedp.Run(ctx,
		chromedp.Navigate(activityUrl),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.OuterHTML(`html`, &body, chromedp.ByQuery),
	)
	if err != nil {
		return nil, err
	}

	c.Logger.Println("Analyzing HTML to find torrents with 'Stopped' status...")
	var rows []*cdp.Node
	err = chromedp.Run(ctx,
		chromedp.Nodes(`div[class^="hnr_all"]`, &rows, chromedp.ByQueryAll),
	)
	if err != nil {
		return nil, err
	}
	c.Logger.Printf("Found %d rows in total.", len(rows))

	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	var matches []string
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" {
			for _, attr := range n.Attr {
				if attr.Key == "class" && (strings.HasPrefix(attr.Val, "hnr_all") || strings.HasPrefix(attr.Val, "hnr_all2")) {
					if containsStopped(n) {
						var buf strings.Builder
						html.Render(&buf, n)
						matches = append(matches, buf.String())
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			f(child)
		}
	}
	f(doc)

	// Log matches
	for i, div := range matches {
		c.Logger.Printf("Found div #%d:\n%s\n\n", i+1, div)
	}

	return matches, nil
}

func (c *Checker) processMatches(ctx context.Context, matches []string) []string {
	var downloaded []string
	for i, match := range matches {
		c.Logger.Printf("Row %d: %s", i+1, match)

		linkRegex := regexp.MustCompile(`<a href="(torrents\.php\?action=details[^"]*)"`)
		linkMatch := linkRegex.FindStringSubmatch(match)

		if len(linkMatch) > 1 {
			c.Logger.Println("Opening torrent page:", linkMatch[1], "Match:", match)
			torrentLink := linkMatch[1]
			torrentUrl := "https://ncore.pro/" + strings.ReplaceAll(torrentLink, "&amp;", "&")

			fileName, err := c.downloadTorrent(ctx, torrentUrl, match)
			if err == nil && fileName != "" {
				downloaded = append(downloaded, fileName)
			}
		}
	}
	return downloaded
}

func (c *Checker) downloadTorrent(ctx context.Context, torrentUrl string, match string) (string, error) {
	var body string
	err := chromedp.Run(ctx,
		chromedp.Navigate(torrentUrl),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.OuterHTML(`html`, &body, chromedp.ByQuery),
	)
	if err != nil {
		c.Logger.Println("Error opening the page:", err)
		return "", err
	}

	// Search and download torrent link
	fileNameRegex := regexp.MustCompile(`<a[^>]*title="([^"]+)"`)
	fileNameMatch := fileNameRegex.FindStringSubmatch(match)
	if len(fileNameMatch) < 2 {
		// Fallback or just return empty if name not found, though regex in original code assumed it exists.
		c.Logger.Println("Could not find filename in match")
		return "", fmt.Errorf("filename not found")
	}
	rawFileName := fileNameMatch[1]

	linkRegex := regexp.MustCompile(`<div class="download">.*?<a [^>]*href="(torrents\.php\?action=download[^"]*)"`)
	linkMatch := linkRegex.FindStringSubmatch(body)

	if len(linkMatch) > 1 {
		downloadLink := linkMatch[1]
		downloadUrl := "https://ncore.pro/" + strings.ReplaceAll(downloadLink, "&amp;", "&")
		c.Logger.Println("Opening torrent page:", torrentUrl, "Torrent download link:", downloadUrl, "file name:", rawFileName)

		finalName := rawFileName + ".torrent"
		if err := c.downloadFile(downloadUrl, finalName); err != nil {
			return "", err
		}
		return finalName, nil
	}
	return "", fmt.Errorf("download link not found")
}

func (c *Checker) downloadFile(downloadUrl string, fileName string) error {
	sanitizeFileName := func(name string) string {
		name = strings.ReplaceAll(name, "?", "_")
		name = strings.ReplaceAll(name, "&", "_")
		name = strings.ReplaceAll(name, "=", "_")
		return name
	}
	c.Logger.Println("Downloading file:", downloadUrl)
	client := &http.Client{}
	req, err := http.NewRequest("GET", downloadUrl, nil)
	if err != nil {
		c.Logger.Println("Error:", err)
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		c.Logger.Println("Error:", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Logger.Println("Error:", resp.StatusCode)
		return fmt.Errorf("status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.Logger.Println("Error:", err)
		return err
	}

	outputPath := filepath.Join(c.OutputDir, sanitizeFileName(fileName))

	if err := os.MkdirAll(c.OutputDir, os.ModePerm); err != nil {
		c.Logger.Println("Error:", err)
		return err
	}

	if err := os.WriteFile(outputPath, body, 0644); err != nil {
		c.Logger.Println("Error:", err)
		return err
	}

	c.Logger.Println("File successfully downloaded and saved:", outputPath)
	return nil
}

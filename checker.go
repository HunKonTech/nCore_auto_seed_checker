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
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"golang.org/x/net/html"

	"ncore_automation/api"
)

type Checker struct {
	User      string
	Pass      string
	OutputDir string
	Debug     bool
	Logger    *log.Logger
	// Cookies, when set, are used instead of the login form (which has a
	// reCAPTCHA). User and Pass are only used if nCore rejects them.
	Cookies []StoredCookie
	// CookieStatus is set by login to "valid" or "rejected" when Cookies were tried.
	CookieStatus string
	// OnFrame, when set, receives live screenshots of the browser during Run.
	OnFrame func(*api.Frame)
}

// TestLogin only logs in, to check whether the saved cookies still work.
func (c *Checker) TestLogin() error {
	ctx, cancel := c.createContext()
	defer cancel()
	return c.login(ctx)
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
	c.startScreencast(ctx)

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

// allocatorOptions are the Chrome flags shared by the checker and the login browser.
func allocatorOptions(extra ...chromedp.ExecAllocatorOption) []chromedp.ExecAllocatorOption {
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
	return append(opts, extra...)
}

func (c *Checker) createContext() (context.Context, context.CancelFunc) {
	// If you wanted to see the browser, you'd remove Headless. For now default is headless.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), allocatorOptions()...)
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
	if len(c.Cookies) > 0 {
		c.Logger.Println("Logging in with session cookie...")
		err := chromedp.Run(ctx, network.SetCookies(cookieParams(c.Cookies)))
		if err != nil {
			return err
		}
		if err = c.verifyLogin(ctx); err == nil {
			c.CookieStatus = "valid"
			return nil
		}
		c.CookieStatus = "rejected"
		if c.User == "" || c.Pass == "" {
			return fmt.Errorf("session cookie rejected, log in again on the Settings page: %w", err)
		}
		c.Logger.Println("Session cookie rejected, falling back to username and password:", err)
		if err := chromedp.Run(ctx, network.ClearBrowserCookies()); err != nil {
			return err
		}
	}
	if c.User == "" || c.Pass == "" {
		return fmt.Errorf("no session cookie or username/password configured")
	}

	var body, url string
	c.Logger.Println("Attempting to log in...")
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
	return c.verifyLogin(ctx)
}

func (c *Checker) verifyLogin(ctx context.Context) error {
	var body, url string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(activityUrl),
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Location(&url),
		chromedp.OuterHTML(`html`, &body, chromedp.ByQuery),
	); err != nil {
		return err
	}
	if strings.Contains(url, "login.php") || (c.User != "" && !strings.Contains(body, c.User)) {
		return fmt.Errorf("login failed, landed on %s", url)
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

// startScreencast streams JPEG screenshots of the browser to OnFrame.
func (c *Checker) startScreencast(ctx context.Context) {
	if c.OnFrame == nil {
		return
	}
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		e, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}
		f := &api.Frame{Data: e.Data}
		if e.Metadata != nil {
			f.Width, f.Height = e.Metadata.DeviceWidth, e.Metadata.DeviceHeight
		}
		c.OnFrame(f)
		go chromedp.Run(ctx, page.ScreencastFrameAck(e.SessionID))
	})
	if err := chromedp.Run(ctx, page.StartScreencast().WithFormat(page.ScreencastFormatJpeg).WithQuality(50).WithMaxWidth(1024).WithMaxHeight(768)); err != nil {
		c.Logger.Println("Preview not available:", err)
	}
}

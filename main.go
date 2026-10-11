package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"golang.org/x/net/html"
)

var (
	loginUrl    = "https://ncore.pro/login.php?honnan=/hitnrun.php"
	activityUrl = "https://ncore.pro/hitnrun.php"
)

func main() {
	debug := flag.Bool("d", false, "Enable debug logging to log.txt")
	user := flag.String("u", "", "nCore username")
	pass := flag.String("p", "", "nCore password")
	outDir := flag.String("o", "", "Directory to store downloaded torrents")
	webMode := flag.Bool("web", false, "Start web interface")
	interval := flag.Duration("interval", 0, "Check interval (e.g. 10m). If 0, runs once.")
	port := flag.Int("port", 8080, "Web server port")
	flag.Parse()

	cfg := Config{
		User:       *user,
		Pass:       *pass,
		Debug:      *debug,
		OutputDir:  *outDir,
		Interval:   *interval,
		EnvCookies: parseCookieString(os.Getenv("NCORE_COOKIE")),
	}
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "interval" {
			cfg.IntervalSet = true
		}
	})

	settings, err := LoadSettings()
	if err != nil {
		fmt.Println("Error reading", settingsFile+":", err)
		os.Exit(1)
	}

	// Without the web interface everything has to come from the command line
	// (or a cookie saved earlier from the web interface).
	if !*webMode {
		hasCookie := len(cfg.EnvCookies) > 0 || len(settings.Get().Cookies) > 0
		if ((*user == "" || *pass == "") && !hasCookie) || (*outDir == "" && settings.Get().OutputDir == "") {
			fmt.Println("username and password (or a session cookie) and output directory are required")
			flag.Usage()
			return
		}
	}

	var hub *Hub
	if *webMode {
		hub = NewHub()
	}
	logs := &LogStream{hub: hub}
	out := io.MultiWriter(os.Stdout, logs)
	if *debug {
		logFile, err := os.OpenFile("log.txt", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Println("Error opening log file:", err)
			os.Exit(1)
		}
		defer logFile.Close()
		out = io.MultiWriter(out, logFile)
	}
	logger := log.New(out, "", log.LstdFlags)
	log.SetOutput(out)

	history, err := NewHistoryManager()
	if err != nil {
		logger.Println("Failed to load history manager:", err)
		os.Exit(1)
	}

	app := NewApp(cfg, logger, hub, logs, history, settings)
	logger.Println("Starting application...")

	if *webMode {
		go startWebServer(*port, hub) // Run web server in a goroutine
	}

	if *webMode || *interval > 0 {
		if d := app.interval(); d > 0 {
			logger.Printf("Running in loop mode. Checking every %v...", d)
		}
		app.Loop()
		return
	}
	app.runCheck()
}

// Helper needed for both
func containsStopped(n *html.Node) bool {
	if n.Type == html.ElementNode && n.Data == "span" {
		for _, attr := range n.Attr {
			if attr.Key == "class" && attr.Val == "stopped" {
				if n.FirstChild != nil && n.FirstChild.Type == html.TextNode && strings.TrimSpace(n.FirstChild.Data) == "Stopped" {
					return true
				}
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if containsStopped(c) {
			return true
		}
	}
	return false
}

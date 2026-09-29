package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const version = "1.0.1"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "featurize":
			runFeaturize()
			return
		case "evaluate":
			runEvaluate()
			return
		}
	}
	addr := flag.String("addr", "127.0.0.1:8430", "address to listen on (use 0.0.0.0:8430 to allow other devices, together with -password)")
	dataDir := flag.String("data", defaultDataDir(), "folder where assignments, submissions and reports are kept")
	password := flag.String("password", "", "require this password (needed when -addr is not localhost)")
	noBrowser := flag.Bool("no-browser", false, "don't open the browser on start")
	flag.Parse()

	host, port, _ := net.SplitHostPort(*addr)
	local := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if !local && *password == "" {
		fmt.Println("To open OriginCheck to other devices you must also set a password, e.g.:")
		fmt.Println("  origincheck.exe -addr 0.0.0.0:8430 -password choose-a-password")
		os.Exit(1)
	}
	openURL := "http://127.0.0.1:" + port + "/"

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		// Probably already running: just open it.
		fmt.Println("OriginCheck seems to be running already at", openURL)
		if !*noBrowser {
			openBrowser(openURL)
		}
		time.Sleep(2 * time.Second)
		return
	}
	st, err := OpenStore(*dataDir)
	if err != nil {
		log.Fatal("Can't open the data folder: ", err)
	}
	loadModel()
	srv := &Server{st: st, ck: NewChecker(st), password: *password, localOnly: local}
	fmt.Printf("OriginCheck %s is running.\n\n  Open %s in your browser.\n  Your data is kept in %s\n\nLeave this window open while you use it. Close it to stop OriginCheck.\n", version, openURL, *dataDir)
	if !*noBrowser {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(openURL)
		}()
	}
	log.Fatal(http.Serve(ln, srv.routes()))
}

func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "OriginCheck")
	}
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "OriginCheck-data")
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		if _, err := exec.LookPath("xdg-open"); err != nil {
			return
		}
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil && !strings.Contains(err.Error(), "not found") {
		log.Println("Couldn't open the browser:", err)
	}
}

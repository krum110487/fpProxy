package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/krum110487/zipfs"
)

type ProxySettings struct {
	LegacyHTDOCSPath    string            `json:"legacyHTDOCSPath"`
	LegacyCGIBINPath    string            `json:"legacyCGIBINPath"`
	LegacyPHPPath       string            `json:"legacyPHPPath"`
	AllowCrossDomain    bool              `json:"allowCrossDomain"`
	VerboseLogging      bool              `json:"verboseLogging"`
	ProxyPort           string            `json:"proxyPort"`
	LegacyServerPort    string            `json:"legacyServerPort"`
	LegacyLoadPHPServer bool              `json:"legacyUsePHPServer"`
	ServerHTTPPort      string            `json:"serverHTTPPort"`
	ServerHTTPSPort     string            `json:"serverHTTPSPort"`
	GameRootPath        string            `json:"gameRootPath"`
	ExternalFilePaths   []string          `json:"externalFilePaths"`
	ExtScriptTypes      []string          `json:"extScriptTypes"`
	ExtMimeTypes        map[string]string `json:"extMimeTypes"`
}

// ExtApplicationTypes is a map that holds the content types of different file extensions
var proxySettings ProxySettings
var proxy *goproxy.ProxyHttpServer
var cwd string

func init() {
	// Load the content types from the JSON file
	data, err := ioutil.ReadFile("proxySettings.json")
	if err != nil {
		panic(err)
	}

	// Unmarshal the JSON data into a Config struct
	err = json.Unmarshal(data, &proxySettings)
	if err != nil {
		panic(err)
	}

	//Get the CWD of this application
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	cwd = strings.ReplaceAll(filepath.Dir(exe), "\\", "/")

	//Get all of the paramaters passed in.
	verboseLogging := flag.Bool("v", false, "should every proxy request be logged to stdout")
	proxyPort := flag.Int("proxyPort", 22500, "proxy listen port")
	legacyServerPort := flag.Int("legacyServerPort", 22600, "port that the legacy server listens on")
	legacyLoadPHPServer := flag.Bool("legacyLoadPHPServer", false, "This will run the original PHP script in parallel")
	legacyHTDOCSPath := flag.String("legacyHTDOCSPath", "D:\\Flashpoint 11 Infinity\\Legacy\\htdocs", "This is the path for HTDOCS")
	legacyPHPPath := flag.String("legacyPHPPath", "D:\\Flashpoint 11 Infinity\\Legacy", "This is the path for HTDOCS")
	serverHTTPPort := flag.Int("serverHttpPort", 22501, "zip server http listen port")
	serverHTTPSPort := flag.Int("serverHttpsPort", 22502, "zip server https listen port")
	gameRootPath := flag.String("gameRootPath", "D:\\Flashpoint 11 Infinity\\Data\\Games", "This is the path where to find the zips")
	flag.Parse()

	//Apply all of the flags to the settings
	proxySettings.VerboseLogging = *verboseLogging
	proxySettings.ProxyPort = strconv.Itoa(*proxyPort)
	proxySettings.LegacyServerPort = strconv.Itoa(*legacyServerPort)
	proxySettings.LegacyLoadPHPServer = *legacyLoadPHPServer
	proxySettings.LegacyHTDOCSPath = *legacyHTDOCSPath
	proxySettings.LegacyPHPPath = *legacyPHPPath
	proxySettings.ServerHTTPPort = strconv.Itoa(*serverHTTPPort)
	proxySettings.ServerHTTPSPort = strconv.Itoa(*serverHTTPSPort)
	proxySettings.GameRootPath = *gameRootPath

	//Setup the proxy.
	proxy = goproxy.NewProxyHttpServer()
	proxy.Verbose = proxySettings.VerboseLogging
	gamePath, _ := normalizePath("", proxySettings.GameRootPath, false)
	fmt.Printf("Proxy Server Started on port %s\n", proxySettings.ProxyPort)
	fmt.Printf("Zip Server Started\n\tHTTP Port: %s\n\tHTTPS Port: %s\n\tGame Root: %s\n",
		proxySettings.ServerHTTPPort,
		proxySettings.ServerHTTPSPort,
		gamePath)
}

func setContentType(r *http.Request, resp *http.Response) {
	if r == nil || resp == nil {
		return
	}

	ext := filepath.Ext(r.URL.Path)
	if ext == "" {
		ext = ".default"
	}

	resp.Header.Set("Content-Type", proxySettings.ExtMimeTypes[ext[1:]])
}

func main() {
	//Handle the re-routing to local files or what not.
	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		fmt.Printf("Proxy Request: %s\n", r.URL.Host+r.URL.Path)
		newURL := *r.URL
		if r.TLS == nil {
			//HTTP request
			newURL.Path = "content/" + r.URL.Host + r.URL.Path
			newURL.Host = "127.0.0.1:" + proxySettings.ServerHTTPPort
		} else {
			//HTTPS request, currently goes to the same server
			newURL.Path = "content/" + r.URL.Host + r.URL.Path
			newURL.Host = "127.0.0.1:" + proxySettings.ServerHTTPSPort
		}

		//Make the request to the zip server.
		client := &http.Client{}
		proxyReq, err := http.NewRequest(r.Method, newURL.String(), r.Body)
		proxyReq.Header = r.Header
		proxyResp, err := client.Do(proxyReq)

		if proxyResp.StatusCode < 400 {
			fmt.Printf("\tServing from Zip...\n")
		}

		//Check Legacy
		if proxyResp.StatusCode >= 400 {
			fmt.Printf("\tServing from Legacy...\n")
			dialer := &net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				// DualStack: true, // this is deprecated as of go 1.16
			}

			proxyURL, _ := url.Parse("127.0.0.1:" + proxySettings.LegacyServerPort)
			proxy := http.ProxyURL(proxyURL)
			transport := &http.Transport{Proxy: proxy}
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				addr = "127.0.0.1:" + proxySettings.LegacyServerPort
				return dialer.DialContext(ctx, network, addr)
			}
			client := &http.Client{Transport: transport, Timeout: 300 * time.Second}
			r.RequestURI = ""
			proxyResp, err = client.Do(r)
		}

		//An error occured, log it for debug purposes
		if err != nil {
			fmt.Printf("UNHANDLED ERROR: %s\n", err)
		}

		//Update the content type based upon ext for now.
		setContentType(r, proxyResp)
		return r, proxyResp
	})

	//Start ZIP server
	go func() {
		log.Fatal(http.ListenAndServe(":"+proxySettings.ServerHTTPPort, zipfs.EmptyFileServer("fpProxy/api/", "", proxySettings.VerboseLogging)))
	}()

	//Start Legacy server
	go func() {
		if proxySettings.LegacyLoadPHPServer {
			phpPath := filepath.Join(proxySettings.LegacyPHPPath, "php")
			cmd := exec.Command(phpPath, "-S", "127.0.0.1:"+proxySettings.LegacyServerPort, "router.php")
			cmd.Dir = proxySettings.LegacyPHPPath
			stdout, _ := cmd.StdoutPipe()
			stderr, _ := cmd.StderrPipe()
			cmd.Start()

			c := make(chan os.Signal, 2)
			signal.Notify(c, os.Interrupt, os.Kill)
			go func() {
				<-c
				// cleanup
				cmd.Process.Kill()
				os.Exit(1)
			}()

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				s := bufio.NewScanner(stdout)
				for s.Scan() {
					fmt.Println(s.Text())
				}
				wg.Done()
			}()

			wg.Add(1)
			go func() {
				s := bufio.NewScanner(stderr)
				for s.Scan() {
					fmt.Println(s.Text())
				}
				wg.Done()
			}()

			wg.Wait()
		} else {
			//log.Fatal(http.ListenAndServe(":"+proxySettings.LegacyServerPort, getLegacyProxy()))
		}
	}()

	//Start PROXY server
	log.Fatal(http.ListenAndServe(":"+proxySettings.ProxyPort, proxy))
}

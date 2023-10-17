package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
	AllowCrossDomain          bool              `json:"allowCrossDomain"`
	VerboseLogging            bool              `json:"verboseLogging"`
	ProxyPort                 string            `json:"proxyPort"`
	ServerHTTPPort            string            `json:"serverHTTPPort"`
	ServerHTTPSPort           string            `json:"serverHTTPSPort"`
	GameRootPath              string            `json:"gameRootPath"`
	ApiPrefix                 string            `json:"apiPrefix"`
	ExternalFilePaths         []string          `json:"externalFilePaths"`
	ExtScriptTypes            []string          `json:"extScriptTypes"`
	ExtIndexTypes             []string          `json:"extIndexTypes"`
	ExtMimeTypesZipFS         map[string]string `json:"extMimeTypesZipFS"`
	ExtMimeTypesProxyOverride map[string]string `json:"extMimeTypesProxyOverride"`
	UseMad4FP                 bool              `json:"useMad4FP"`
	LegacyGoPort              string            `json:"legacyGoPort"`
	LegacyPHPPort             string            `json:"legacyPHPPort"`
	LegacyPHPPath             string            `json:"legacyPHPPath"`
	LegacyUsePHPServer        bool              `json:"legacyUsePHPServer"`
	LegacyHTDOCSPath          string            `json:"legacyHTDOCSPath"`
	LegacyCGIBINPath          string            `json:"legacyCGIBINPath"`
}

// ExtApplicationTypes is a map that holds the content types of different file extensions
var proxySettings ProxySettings
var proxy *goproxy.ProxyHttpServer
var cwd string

func init() {
	// Load the content types from the JSON file
	data, err := os.ReadFile("proxySettings.json")
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

	//TODO: Update proxySettings.LegacyHTDOCSPath AND proxySettings.LegacyPHPPath for the default values!

	//Get all of the paramaters passed in.
	verboseLogging := flag.Bool("v", false, "should every proxy request be logged to stdout")
	proxyPort := flag.Int("proxyPort", 22500, "proxy listen port")
	serverHTTPPort := flag.Int("serverHttpPort", 22501, "zip server http listen port")
	serverHTTPSPort := flag.Int("serverHttpsPort", 22502, "zip server https listen port")
	gameRootPath := flag.String("gameRootPath", "D:\\Flashpoint 11 Infinity\\Data\\Games", "This is the path where to find the zips")
	apiPrefix := flag.String("apiPrefix", proxySettings.ApiPrefix, "apiPrefix is used to prefix any API call.")
	useMad4FP := flag.Bool("UseMad4FP", false, "flag to turn on/off Mad4FP.")
	legacyGoPort := flag.Int("legacyGoPort", 22601, "port that the legacy GO server listens on")
	legacyPHPPort := flag.Int("legacyPHPPort", 22600, "port that the legacy PHP server listens on")
	legacyPHPPath := flag.String("legacyPHPPath", "D:\\Flashpoint 11 Infinity\\Legacy", "This is the path for HTDOCS")
	legacyUsePHPServer := flag.Bool("legacyUsePHPServer", true, "This will run the original PHP script in parallel")
	legacyHTDOCSPath := flag.String("legacyHTDOCSPath", "D:\\Flashpoint 11 Infinity\\Legacy\\htdocs", "This is the path for HTDOCS")

	flag.Parse()

	//Apply all of the flags to the settings
	proxySettings.VerboseLogging = *verboseLogging
	proxySettings.ProxyPort = strconv.Itoa(*proxyPort)
	proxySettings.ServerHTTPPort = strconv.Itoa(*serverHTTPPort)
	proxySettings.ServerHTTPSPort = strconv.Itoa(*serverHTTPSPort)
	proxySettings.GameRootPath = *gameRootPath
	proxySettings.ApiPrefix = *apiPrefix
	proxySettings.UseMad4FP = *useMad4FP
	proxySettings.LegacyGoPort = strconv.Itoa(*legacyGoPort)
	proxySettings.LegacyPHPPort = strconv.Itoa(*legacyPHPPort)
	proxySettings.LegacyPHPPath = *legacyPHPPath
	proxySettings.LegacyUsePHPServer = *legacyUsePHPServer
	proxySettings.LegacyHTDOCSPath = *legacyHTDOCSPath

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

	//Get the ZipFS and Request Extensions if they have it.
	rext := filepath.Ext(strings.ToLower(resp.Header.Get("ZIPSVR_FILENAME")))
	ext := filepath.Ext(strings.ToLower(r.URL.Path))

	//If the request already has an extension, just use it, only when setup in the ProxyOverride
	if ext != "" {
		resp.Header.Set("Content-Type", proxySettings.ExtMimeTypesProxyOverride[ext[1:]])
		return
	}

	//If the response has an extension, use that.
	if rext != "" {
		resp.Header.Set("Content-Type", proxySettings.ExtMimeTypesProxyOverride[rext[1:]])
		return
	}

	//Finally, just use the default type IF it exists AND it is not already set.
	existingMime := resp.Header.Get("Content-Type")
	defExt, ok := proxySettings.ExtMimeTypesProxyOverride["default"]
	if ok && existingMime == "" {
		resp.Header.Set("Content-Type", defExt)
	}
}

func main() {
	proxy.NonproxyHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		//TODO: Needs Actual implementation of described below
		//This will be used to handle the response from zipfs instead of the launcher sending it
		//directly to the zipfs, it would be forwarded through this handler.

		//Handle the endpoints here and forward it to zipfs, intercept the response
		//If the response for adding a zip is < 400 then we will intercept it and grab the body
		//which contains a list of Paths for the redirect and other configs for the proxy.
		//Then we hit the zipfs server to pull the files contents and parse it
		//Once it is parsed and ready THEN return the response to the front-end.
		//This will make the front-end wait until we are ready to continue.
		http.Error(w, "The endpoint is invalid.", 500)
	})

	//Handle the re-routing to local files or what not.
	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		fmt.Printf("Proxy Request: %s\n", r.URL.Host+r.URL.Path)
		newURL := *r.URL
		newPath, _ := url.PathUnescape(r.URL.Path)
		newURL.Path = "content/" + strings.TrimSpace(r.URL.Host+newPath)
		if r.TLS == nil {
			//HTTP request
			newURL.Host = "127.0.0.1:" + proxySettings.ServerHTTPPort
		} else {
			//HTTPS request, currently goes to the same server
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

			//Decide on the port to use
			port := proxySettings.LegacyGoPort
			if proxySettings.LegacyUsePHPServer {
				port = proxySettings.LegacyPHPPort
			}

			//Set the Proxy URL and apply it to the Transpor layer so that the request respects the proxy.
			proxyURL, _ := url.Parse("http://127.0.0.1:" + port)
			proxy := http.ProxyURL(proxyURL)
			transport := &http.Transport{Proxy: proxy}

			//A custom Dialer is required for the "localflash" urls, instead of using the DNS, we use this.
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				//Set Dialer timeout and keepalive to 30 seconds and force the address to localhost.
				dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
				addr = "127.0.0.1:" + port
				return dialer.DialContext(ctx, network, addr)
			}

			//TODO: Investigate if I need to blank this out... I don't think this is required.
			r.RequestURI = ""

			//Make the request with the custom transport.
			client := &http.Client{Transport: transport, Timeout: 300 * time.Second}
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
		//TODO: Update these to be modifiable in the properties json.
		//TODO: Also update the "fpProxy/api/" to be in the properties json.
		log.Fatal(http.ListenAndServe(":"+proxySettings.ServerHTTPPort,
			zipfs.EmptyFileServer(
				proxySettings.ApiPrefix,
				"",
				proxySettings.VerboseLogging,
				proxySettings.ExtIndexTypes,
				proxySettings.ExtMimeTypesZipFS,
			),
		))
	}()

	//Start Legacy server
	go func() {
		if proxySettings.LegacyUsePHPServer {
			runLegacyPHP()
		} else {
			log.Fatal(http.ListenAndServe(":"+proxySettings.LegacyGoPort, getLegacyProxy()))
		}
	}()

	//Start PROXY server
	log.Fatal(http.ListenAndServe(":"+proxySettings.ProxyPort, http.AllowQuerySemicolons(proxy)))
}

func runLegacyPHP() {
	phpPath := filepath.Join(proxySettings.LegacyPHPPath, "php")
	cmd := exec.Command(phpPath, "-S", "127.0.0.1:"+proxySettings.LegacyPHPPort, "router.php")
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
}

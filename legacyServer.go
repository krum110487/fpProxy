package main

import (
	"bytes"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/elazarl/goproxy"
)

var legacyProxy *goproxy.ProxyHttpServer

func init() {
	legacyProxy = goproxy.NewProxyHttpServer()
	legacyProxy.Verbose = proxySettings.VerboseLogging
}

func normalizePath(rootPath string, pathOrURL string, useCWD bool) (string, error) {
	normPath := ""

	//Step0: Check if path is a URL
	u, err := url.Parse(pathOrURL)
	if err == nil && (strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://")) {
		pathOrURL, _ = url.JoinPath(u.Host, u.Path)
	}

	//Step1: Normalize all of the slashes
	pathOrURL = strings.Replace(pathOrURL, "\\", "/", -1)
	if rootPath != "" {
		rootPath = strings.Replace(rootPath, "\\", "/", -1)
	}

	//Step2: Join the path
	if !filepath.IsAbs(pathOrURL) {
		normPath = filepath.Join(rootPath, pathOrURL)
	} else {
		return pathOrURL, nil
	}

	//Step4: Check if absolute
	if !filepath.IsAbs(normPath) {
		rPath, _ := os.Getwd()
		if !useCWD {
			rPath, _ = os.Executable()
		}
		dir := filepath.Dir(strings.Replace(rPath, "\\", "/", -1))
		normPath = filepath.Join(dir, normPath)
	}

	// Clean the path to prevent multiple slashes
	return filepath.Clean(normPath), nil
}

func openIfExists(filePath string) (*os.File, error) {
	_, err := os.Stat(filePath)

	if err == nil {
		// File exists, open it
		file, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}

		return file, nil
	} else if os.IsNotExist(err) {
		// File does not exist
		return nil, nil
	} else {
		// Some other error occurred
		return nil, err
	}
}

func getIndexFile(urlPath string) *os.File {
	var localFile *os.File
	indexHtmlPath, _ := url.JoinPath(urlPath, "index.html")
	fileHtmlpath, _ := normalizePath(proxySettings.LegacyHTDOCSPath, indexHtmlPath, false)
	localFile, _ = openIfExists(fileHtmlpath)
	if localFile == nil {
		return localFile
	}

	indexHtmPath, _ := url.JoinPath(urlPath, "index.htm")
	fileHtmpath, _ := normalizePath(proxySettings.LegacyHTDOCSPath, indexHtmPath, false)
	localFile, _ = openIfExists(fileHtmpath)
	if localFile == nil {
		return localFile
	}

	return nil
}

func getRemoteIndexFile(urlPath string, r *http.Request) *http.Response {
	indexHtmlPath, _ := url.JoinPath(urlPath, "index.html")
	for _, baseURL := range proxySettings.ExternalFilePaths {
		resp := getRemoteFile(baseURL, proxySettings.LegacyHTDOCSPath, indexHtmlPath, *r)
		if resp != nil && resp.StatusCode != 404 {
			return resp
		}
	}

	indexHtmPath, _ := url.JoinPath(urlPath, "index.htm")
	for _, baseURL := range proxySettings.ExternalFilePaths {
		resp := getRemoteFile(baseURL, proxySettings.LegacyHTDOCSPath, indexHtmPath, *r)
		if resp != nil && resp.StatusCode != 404 {
			return resp
		}
	}

	return nil
}

func readFile(file *os.File) (io.ReadCloser, error) {
	// Get the file size and rewind the file pointer to the beginning
	size, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return nil, err
	}

	// Create a byte slice with the same size as the file
	contents := make([]byte, size)

	// Read the entire file contents into the byte slice
	_, err = file.Read(contents)
	if err != nil {
		return nil, err
	}

	// Convert the byte slice to an io.ReadCloser object
	reader := bytes.NewReader(contents)
	return ioutil.NopCloser(reader), nil
}

func getRemoteFile(urlPrefix string, localRootDir string, urlPath string, originalRequest http.Request) *http.Response {
	// Download the file from the URL and write it to the output file
	client := &http.Client{}
	newURL, _ := url.JoinPath(urlPrefix, urlPath)
	remoteReq, err := http.NewRequest(originalRequest.Method, newURL, originalRequest.Body)
	response, err := client.Do(remoteReq)
	if err != nil {
		return nil
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil
	}

	// Get the filename from the URL
	localPath, _ := normalizePath(localRootDir, urlPath, false)

	// Get the directory path from the URL and create any missing directories
	dirPath := filepath.Dir(localPath)
	err = os.MkdirAll(dirPath, 0755)
	if err != nil {
		return nil
	}

	// Create the output file
	file, err := os.Create(localPath)
	if err != nil {
		return nil
	}
	defer file.Close()

	_, err = io.Copy(file, response.Body)
	if err != nil {
		return nil
	}

	return response
}

func getLegacyProxy() *goproxy.ProxyHttpServer {
	legacyProxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		var localFile *os.File = nil
		errResp := goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusNotFound, "404 Not Found")
		reqUrl := *r.URL
		reqUrlExt := filepath.Ext(reqUrl.Path)

		log.Printf("Legacy Request Started: %s", reqUrl)

		//Step 1: Try to find the file, in the root folder
		if reqUrlExt != "" {
			fp, err := normalizePath(proxySettings.LegacyHTDOCSPath, reqUrl.Path, false)
			if err != nil {
				//TODO: Throw Error here
			}
			localFile, err = openIfExists(fp)
			if localFile == nil {
				return r, getRemoteFile("", proxySettings.LegacyHTDOCSPath, reqUrl.Path, *r)
			}
		} else {
			localFile = getIndexFile(reqUrl.Path)
			if localFile == nil {
				getRemoteIndexFile(reqUrl.Path, r)
			}
		}

		//Step 2: if the file is not found, return here, it isn't found locally:
		if localFile == nil {
			return r, errResp
		}

		//Step 3: If the file is found, we need to create a response
		proxyResp := goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusOK, "")
		contents, err := readFile(localFile)
		if err != nil {
			//TODO: Throw Error
			//need to retry...
			return r, errResp
		}

		//Step 4: Set the contents of the body and return it.
		proxyResp.Body = contents
		return r, proxyResp
	})

	return legacyProxy
}

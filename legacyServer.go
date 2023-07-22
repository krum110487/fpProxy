package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/elazarl/goproxy"
)

var legacyProxy *goproxy.ProxyHttpServer

func init() {
	legacyProxy = goproxy.NewProxyHttpServer()
	legacyProxy.Verbose = proxySettings.VerboseLogging
}

func getLegacyProxy() *goproxy.ProxyHttpServer {
	legacyProxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		var localFile *os.File = nil

		errResp := goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusNotFound, "404 Not Found")
		reqUrl := *r.URL
		extensions := []string{".html", ".htm"}
		legacyHTDOCS := proxySettings.LegacyHTDOCSPath
		fmt.Printf("Legacy Request Started: %s", reqUrl.String())

		//Normalize the path...
		newPath := path.Join(reqUrl.Host, reqUrl.Path)
		fp, err := normalizePath(legacyHTDOCS, newPath, false)
		if err != nil {
			//TODO: Throw Error here
		}

		//Try to open the Local File including the indexes which may exist...
		localFile, err = openIfExists(fp, extensions)
		if err == nil {
			//We found a file, so we can create a response!
			proxyResp := goproxy.NewResponse(r, goproxy.ContentTypeText, http.StatusOK, "")
			proxyResp.Header.Set("ZIPSVR_FILENAME", localFile.Name())
			contents, err := readFile(localFile)
			if err != nil {
				return r, errResp
			}
			proxyResp.Body = contents
			return r, proxyResp
		}

		//Only if Mad4FP is enabled...
		if proxySettings.UseMad4FP {
			resp, err := getLiveRemoteFile(legacyHTDOCS, *r)
			if err != nil {
				fmt.Printf("Mad4FP failed with \"%s\"", err)
			}
			if resp.StatusCode < 400 {
				return r, resp
			}
		} else {
			efp := proxySettings.ExternalFilePaths
			resp, err := getRemoteFile(efp, legacyHTDOCS, extensions, *r)
			if err != nil {
				fmt.Printf("getRemoteFile failed with \"%s\"", err)
			}
			if resp.StatusCode < 400 {
				return r, resp
			}
		}

		//Return an error if nothing above worked.
		return r, errResp
	})

	return legacyProxy
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

func openIfExists(filePath string, indexExts []string) (*os.File, error) {
	fi, err := os.Stat(filePath)
	extn := filepath.Ext(filePath)

	//Only do this if the path is found and is a dir OR
	//path is not found and the extension is blank.
	if (err == nil && fi.IsDir()) || (err != nil && extn == "") {
		//Loop through exts and try to find index.
		for _, ext := range indexExts {
			indexFilePath := path.Join(filePath, "/index."+ext)
			fii, inErr := os.Stat(indexFilePath)
			if inErr != nil {
				fi = fii
				break
			}
			err = inErr
		}
	}

	//Index was not found, path is a valid dir.
	if fi != nil && fi.IsDir() {
		return nil, errors.New(fmt.Sprintf("Index cannot be found inside directory %s", filePath))
	}

	//File was found and is NOT a directory, we can serve it.
	if err == nil {
		// File exists, open it
		file, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}

		return file, nil
	} else if os.IsNotExist(err) {
		// File does not exist
		return nil, err
	} else {
		// Some other error occurred
		return nil, err
	}
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

func saveLocalFile(resp *http.Response, localRootDir string, u url.URL) error {
	// Get the filename from the URL
	urlPath := path.Join(u.Host, "/", u.Path)
	localPath, _ := normalizePath(localRootDir, urlPath, false)
	resp.Header.Set("ZIPSVR_FILENAME", localPath)

	// Get the directory path from the URL and create any missing directories
	dirPath := filepath.Dir(localPath)
	err := os.MkdirAll(dirPath, 0755)
	if err != nil {
		return err
	}

	// Create the output file
	file, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

func getLiveRemoteFile(localRootDir string, originalRequest http.Request) (*http.Response, error) {
	return getRemoteFile([]string{}, localRootDir, []string{}, originalRequest)
}

func requestFileAndSave(client *http.Client, prefix string, localRootDir string, u url.URL, origReq http.Request) (*http.Response, error) {
	newURL := u.String()
	if prefix != "" {
		newURL, _ = url.JoinPath(prefix, u.Host, u.Path)
	}

	remoteReq, err := http.NewRequest(origReq.Method, newURL, origReq.Body)
	response, err := client.Do(remoteReq)
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		err := saveLocalFile(response, localRootDir, u)
		if err != nil {
			return nil, err
		}
		return response, nil
	}

	return nil, errors.New("File was not found!")
}

func getRemoteFile(urlPrefix []string, localRootDir string, indexExts []string, origReq http.Request) (*http.Response, error) {
	client := &http.Client{}
	errResp := goproxy.NewResponse(&origReq, goproxy.ContentTypeText, http.StatusNotFound, "404 Not Found")
	var extErr error = nil

	//If the prefix is not set, we are going on the LIVE internet!
	if len(urlPrefix) < 1 {
		//GOING TO THAT LIVE INTERWEBZ!
		resp, err := requestFileAndSave(client, "", localRootDir, *origReq.URL, origReq)
		extErr = err
		if err == nil {
			return resp, nil
		}
	} else {
		//Loop through all the given prefixes
		for _, prefix := range urlPrefix {
			//Try the Original url with the prefix.
			resp, err := requestFileAndSave(client, prefix, localRootDir, *origReq.URL, origReq)
			extErr = err
			if err == nil {
				return resp, nil
			}

			//If the file wasn't found, we append the indexes until it is found.
			for _, ext := range indexExts {
				//Generate url from Remote HTDOCS
				indexURLstr, _ := url.JoinPath(origReq.URL.String(), "/index."+ext)
				newIndexURL, _ := url.Parse(indexURLstr)

				//Get the files.
				resp, err := requestFileAndSave(client, prefix, localRootDir, *newIndexURL, origReq)
				extErr = err
				if err == nil {
					return resp, nil
				}
			}
		}
	}
	return errResp, extErr
}

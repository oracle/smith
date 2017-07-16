package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	gdigest "github.com/opencontainers/go-digest"
)

// RegistryClient is a type for container image registry clients.
type RegistryClient struct {
	http.Client
}

// RepoInfo is a type for container image repositories.
type RepoInfo struct {
	Scheme   string
	Host     string
	Auth     string
	Service  string
	Username string
	Password string
	Reponame string
	Tag      string
	Token    string
	Docker   bool
}

func parseRepoInfo(remote string, docker bool) (*RepoInfo, error) {
	r := RepoInfo{}
	data, err := url.Parse(remote)
	if err != nil {
		return nil, err
	}
	r.Scheme = data.Scheme
	r.Host = data.Host
	r.Docker = docker
	if r.Host == "registry-1.docker.io" {
		// shortcut auth and service for dockerhub
		r.Auth = "https://auth.docker.io/token"
		r.Service = "registry.docker.io"
		r.Docker = true
	}
	if data.User != nil {
		// get username and password
		r.Username = data.User.Username()
		r.Password, _ = data.User.Password()
	}
	r.Tag = "latest"
	if len(data.Path) != 0 {
		// remove the initial / from reponame
		r.Reponame = data.Path[1:]
		parts := strings.SplitN(r.Reponame, ":", 2)
		// extract tag name from path
		if len(parts) > 1 {
			r.Reponame = parts[0]
			r.Tag = parts[1]
		}
	}
	return &r, nil
}

// NewRegistryClient creates a new docker registry client and returns
// a pointer to the client.
func NewRegistryClient(insecure bool) *RegistryClient {
	// DefaultTransort with setable insecure value
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecure},
	}
	return &RegistryClient{http.Client{Transport: tr}}
}

func uploadContainer(inName, remote string, insecure bool, docker bool) bool {
	r := NewRegistryClient(insecure)
	info, err := parseRepoInfo(remote, docker)
	if err != nil {
		logrus.Errorf("Failed to parse repository info for %s: %v", remote, err)
		return false
	}
	image, err := imageFromFile(inName)
	if err != nil {
		logrus.Errorf("Failed to get image from %s: %v", inName, err)
		return false
	}
	if err := r.ImageToRepo(info, image); err != nil {
		logrus.Errorf("Failed to upload image to %s: %v", remote, err)
		return false
	}
	logrus.Infof("Successfully uploaded %s to %s", inName, remote)
	return true
}

// ImageToRepo puts an Image to a repository. It does this by uploading
// the image layers first the config data second and then the manifest third.
func (r *RegistryClient) ImageToRepo(info *RepoInfo, image *Image) error {
	lMT := layerMT
	cMT := configMT
	mMT := manifestMT
	if info.Docker {
		lMT = dockerLayerMT
		cMT = dockerConfigMT
		mMT = dockerManifestMT
	}
	for _, l := range image.Layers {
		p := path.Join("blobs", string(l.Desc.Digest))
		// media type of blob seems to be ignored, but set it just in case
		if err := r.PutObject(info, p, lMT, l.Data); err != nil {
			return err
		}
	}
	configData, err := serializeConfig(image)
	if err != nil {
		return err
	}

	configSha := digest(configData)
	p := path.Join("blobs", string(configSha))
	if err := r.PutObject(info, p, cMT, configData); err != nil {
		return err
	}

	configDesc := desc(cMT, configData, configSha)
	manifestData, err := serializeManifest(configDesc, image.Layers, info.Docker)
	p = path.Join("manifests", info.Tag)
	if err := r.PutObject(info, p, mMT, manifestData); err != nil {
		return err
	}
	return nil
}

func downloadContainer(outName, remote string, insecure bool) bool {
	image, err := imageFromRemote(remote, insecure)
	if err != nil {
		logrus.Errorf("Failed to get image from remote %s: %v", remote, err)
		return false
	}

	// add some metadata
	image.Metadata = getMetadata()
	if err := WriteOciTarGz(image, outName); err != nil {
		logrus.Errorf("Failed to write image to %s: %v", outName, err)
		return false
	}
	logrus.Infof("Successfully downloaded %s to %s", remote, outName)
	return true
}

func imageFromRemote(remote string, insecure bool) (*Image, error) {
	r := NewRegistryClient(insecure)
	info, err := parseRepoInfo(remote, false)
	if err != nil {
		return nil, err
	}
	return r.ImageFromRepo(info)
}

// ImageFromRepo gets an image from a repository.
func (r *RegistryClient) ImageFromRepo(info *RepoInfo) (*Image, error) {
	return imageFromDigest(r.ImageGetter(info), "manifest", nil)
}

// ImageGetter returns a function which gets an object from the
// registry in the registry client it is called on.
func (r *RegistryClient) ImageGetter(info *RepoInfo) Extractor {
	return func(digest gdigest.Digest) ([]byte, error) {
		// registries store the manifest separately
		if string(digest) == "manifest" {
			return r.GetObject(info, path.Join("manifests", info.Tag))
		}
		return r.GetObject(info, path.Join("blobs", string(digest)))
	}
}

func extractAuth(resp *http.Response, info *RepoInfo) error {
	val := resp.Header.Get("WWW-Authenticate")
	parts := strings.Fields(val)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return fmt.Errorf("invalid WWW-Authenticate header: '%s'", val)
	}
	keyvals := strings.Split(parts[1], ",")
	items := map[string]string{}
	for _, keyval := range keyvals {
		pieces := strings.Split(keyval, "=")
		if len(pieces) != 2 {
			return fmt.Errorf("invalid value in WWW-Authenticate header: '%s'", val)
		}
		// strip quotes
		if pieces[1][0] == '"' || pieces[1][0] == '\'' {
			pieces[1] = pieces[1][1 : len(pieces[1])-1]
		}

		items[pieces[0]] = pieces[1]
	}
	info.Auth = items["realm"]
	if info.Auth == "" {
		return fmt.Errorf("realm not found in WWW-Authenticate header: '%s'", val)
	}
	info.Service = items["service"]
	if info.Auth == "" {
		return fmt.Errorf("service not found in WWW-Authenticate header: '%s'", val)
	}
	return nil
}

// GetTokenResponse is a type that provides the json structure of token
// response.
type GetTokenResponse struct {
	Token string `json:"token,omitempty"`
}

// PrepPutObject attempts to auth to the repo and perform a POST. It returns the uploadURL
// that is returned in the headers from a successful auth and post.
func (r *RegistryClient) PrepPutObject(info *RepoInfo, path string) (string, error) {
	// if Auth is not set, we will get a 401 and retry below
	if info.Token == "" && info.Auth != "" {
		if err := r.GetToken(info, []string{"push,pull"}); err != nil {
			logrus.Errorf("Failed to get token for %s: %v", info.Reponame, err)
			return "", err
		}
	}
	digest := path[len("blobs/"):]
	postURL := fmt.Sprintf("%s://%s/v2/%s/blobs/uploads/",
		info.Scheme, info.Host, info.Reponame)
	req, err := http.NewRequest("POST", postURL, nil)
	if err != nil {
		return "", err
	}
	if info.Token != "" {
		req.Header.Set("Authorization", "Bearer "+info.Token)
	}
	logrus.Debugf("Prepping put to %s", postURL)
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		if info.Token != "" {
			return "", fmt.Errorf("Token is invalid for %s", info.Reponame)
		}
		// we don't have a token so extract auth data
		if err := extractAuth(resp, info); err != nil {
			return "", err
		}
		// try again
		logrus.Debugf("Retrying %s with a token", path)
		return r.PrepPutObject(info, path)
	} else if resp.StatusCode != 202 {
		var buf bytes.Buffer
		if _, err = buf.ReadFrom(resp.Body); err != nil {
			logrus.Warnf("Failed to read response body: %v", err)
		}
		return "", fmt.Errorf("Blobs post returned invalid response %d:\n%s",
			resp.StatusCode, string(buf.Bytes()))
	}
	uploadURL := resp.Header.Get("Location")
	if uploadURL == "" {
		return "", fmt.Errorf("Repository did not return an upload url")
	}
	if strings.HasPrefix(uploadURL, "/") {
		uploadURL = fmt.Sprintf("%s://%s%s", info.Scheme, info.Host, uploadURL)
	}
	if strings.Contains(uploadURL, "?") {
		uploadURL += "&digest=" + digest
	} else {
		uploadURL += "?digest=" + digest
	}
	logrus.Debugf("Got %s for %s", uploadURL, path)
	return uploadURL, nil
}

// PutObject puts an object to the repo in "info" at the path in "path".
func (r *RegistryClient) PutObject(info *RepoInfo, path, ct string, data []byte) error {
	if info.Host == "" {
		return fmt.Errorf("Host must be specified")
	}
	if info.Reponame == "" {
		return fmt.Errorf("Reponame must be specified")
	}

	// if Auth is not set, we will get a 401 and retry below
	if info.Token == "" && info.Auth != "" {
		if err := r.GetToken(info, []string{"push,pull"}); err != nil {
			logrus.Errorf("Failed to get token for %s: %v", info.Reponame, err)
			return err
		}
	}
	u := fmt.Sprintf("%s://%s/v2/%s/%s",
		info.Scheme, info.Host, info.Reponame, path)
	if strings.HasPrefix(path, "blobs/") {
		// check for existing blob
		logrus.Debugf("Performing HEAD on %s", path)
		req, err := http.NewRequest("HEAD", u, nil)
		if err != nil {
			return err
		}
		if info.Token != "" {
			req.Header.Set("Authorization", "Bearer "+info.Token)
		}
		resp, err := r.Client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		// we should have a token by this point, but handle 401
		// here as well just in case
		if resp.StatusCode == 401 {
			if info.Token != "" {
				return fmt.Errorf("Token is invalid for %s", info.Reponame)
			}
			// we don't have a token so extract auth data
			if err := extractAuth(resp, info); err != nil {
				return err
			}
			logrus.Debugf("Retrying %s with a token", path)
			return r.PutObject(info, path, ct, data)
		} else if resp.StatusCode == 200 {
			// object exists so bail
			logrus.Infof("Object at %s already exists", path)
			return nil
		} else if resp.StatusCode == 404 {
			// object is not found so prep to put it
			var err error
			u, err = r.PrepPutObject(info, path)
			if err != nil {
				return err
			}
		} else {
			// something went wrong
			var buf bytes.Buffer
			if _, err = buf.ReadFrom(resp.Body); err != nil {
				logrus.Warnf("Failed to read response body: %v", err)
			}
			return fmt.Errorf("Put request returned invalid response %d:\n%s",
				resp.StatusCode, string(buf.Bytes()))
		}
	}
	req, err := http.NewRequest("PUT", u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if info.Token != "" {
		req.Header.Set("Authorization", "Bearer "+info.Token)
	}
	req.Header.Set("Content-Type", ct)
	logrus.Debugf("Uploading %s", path)
	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		if info.Token != "" {
			return fmt.Errorf("Token is invalid for %s", info.Reponame)
		}
		// we don't have a token so extract auth data
		if err := extractAuth(resp, info); err != nil {
			return err
		}
		logrus.Debugf("Retrying %s with a token", path)
		return r.PutObject(info, path, ct, data)
	} else if resp.StatusCode != 201 {
		var buf bytes.Buffer
		if _, err = buf.ReadFrom(resp.Body); err != nil {
			logrus.Warnf("Failed to read response body: %v", err)
		}
		return fmt.Errorf("Put request returned invalid response %d:\n%s",
			resp.StatusCode, string(buf.Bytes()))
	}
	logrus.Infof("Uploaded %s", path)
	return nil
}

// GetObject gets an object at the path specified in "path" from the repo in
// "info".
func (r *RegistryClient) GetObject(info *RepoInfo, path string) ([]byte, error) {
	if info.Host == "" {
		return nil, fmt.Errorf("Host must be specified")
	}
	if info.Reponame == "" {
		return nil, fmt.Errorf("Reponame must be specified")
	}

	// if Auth is not set, we will get a 401 and retry below
	if info.Token == "" && info.Auth != "" {
		if err := r.GetToken(info, []string{"pull"}); err != nil {
			logrus.Errorf("Failed to get token for %s: %v", info.Reponame, err)
			return nil, err
		}
	}
	u := fmt.Sprintf("%s://%s/v2/%s/%s",
		info.Scheme, info.Host, info.Reponame, path)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	if info.Token != "" {
		req.Header.Set("Authorization", "Bearer "+info.Token)
	}
	if strings.HasPrefix(path, "manifests/") {
		// accept oci or dockerv2 type for manifest
		req.Header.Set("Accept", manifestMT+","+dockerManifestMT)
	}
	logrus.Debugf("Downloading %s", path)
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		if info.Token != "" {
			return nil, fmt.Errorf("Token is invalid for %s", info.Reponame)
		}
		// we don't have a token so extract auth data
		if err := extractAuth(resp, info); err != nil {
			return nil, err
		}
		logrus.Debugf("Retrying %s with a token", path)
		return r.GetObject(info, path)
	} else if resp.StatusCode != 200 {
		var buf bytes.Buffer
		if _, err = buf.ReadFrom(resp.Body); err != nil {
			logrus.Warnf("Failed to read response body: %v", err)
		}
		return nil, fmt.Errorf("Get request returned invalid response %d:\n%s",
			resp.StatusCode, string(buf.Bytes()))
	}
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	logrus.Infof("Downloaded %s", path)
	return buf.Bytes(), nil
}

// GetToken gets an authentication token that will be used for authentication
// of calls made on the reciever RegistryClient.
func (r *RegistryClient) GetToken(info *RepoInfo, actions []string) error {
	if info.Host == "" {
		return fmt.Errorf("Host must be specified")
	}
	if info.Reponame == "" {
		return fmt.Errorf("Reponame must be specified")
	}
	act := strings.Join(actions, ",")
	u := fmt.Sprintf("%s?service=%s&scope=repository:%s:%s",
		info.Auth, info.Service, info.Reponame, act)

	req, err := http.NewRequest("GET", u, nil)

	if info.Username != "" {
		userPass := fmt.Sprintf("%s:%s", info.Username, info.Password)
		cred := base64.StdEncoding.EncodeToString([]byte(userPass))
		req.Header.Set("Authorization", "Basic "+cred)
	}
	logrus.Debugf("Making auth request to %s", u)
	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var buf bytes.Buffer
		if _, err = buf.ReadFrom(resp.Body); err != nil {
			logrus.Warnf("Failed to read response body: %v", err)
		}
		return fmt.Errorf("Auth server returned invalid response %d:\n%s",
			resp.StatusCode, string(buf.Bytes()))
	}
	tokenResponse := GetTokenResponse{}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&tokenResponse); err != nil {
		return err
	}
	info.Token = tokenResponse.Token
	return nil
}

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type profileState struct {
	Values    map[string]string `json:"values"`
	Cookies   []storedCookie    `json:"cookies"`
	LastLogin string            `json:"last_login,omitempty"`
}

type scopedStateSet struct {
	options globalOptions
	site    string
	states  map[storageScope]*profileState
}

func newScopedStateSet(options globalOptions, site string) *scopedStateSet {
	return &scopedStateSet{
		options: options,
		site:    site,
		states:  map[storageScope]*profileState{},
	}
}

func (s *scopedStateSet) dir(scope storageScope) (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: state store is not configured", ErrConfig)
	}
	return stateDirForScope(s.options, scope)
}

func (s *scopedStateSet) set(scope storageScope, state *profileState) {
	if s == nil {
		return
	}
	s.states[scope] = state
}

func (s *scopedStateSet) load(scope storageScope) (*profileState, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: state store is not configured", ErrConfig)
	}
	if state, ok := s.states[scope]; ok {
		return state, nil
	}
	dir, err := s.dir(scope)
	if err != nil {
		return nil, err
	}
	lock, err := acquireStateLock(dir, s.site)
	if err != nil {
		return nil, err
	}
	defer lock.Close()

	state, err := loadState(dir, s.site)
	if err != nil {
		return nil, err
	}
	s.states[scope] = state
	return state, nil
}

type storedCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Path     string    `json:"path,omitempty"`
	Domain   string    `json:"domain,omitempty"`
	Expires  time.Time `json:"expires,omitempty"`
	Secure   bool      `json:"secure,omitempty"`
	HTTPOnly bool      `json:"http_only,omitempty"`
}

type persistentJar struct {
	mu      sync.Mutex
	cookies []storedCookie
}

func newPersistentJar(cookies []storedCookie) *persistentJar {
	out := make([]storedCookie, len(cookies))
	copy(out, cookies)
	return &persistentJar{cookies: out}
}

func (j *persistentJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()

	now := time.Now()
	for _, cookie := range cookies {
		stored := storedCookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Path:     cookie.Path,
			Domain:   cookie.Domain,
			Expires:  cookie.Expires,
			Secure:   cookie.Secure,
			HTTPOnly: cookie.HttpOnly,
		}
		if stored.Path == "" {
			stored.Path = defaultCookiePath(u.Path)
		}
		if stored.Domain == "" {
			stored.Domain = u.Hostname()
		}
		j.deleteCookieLocked(stored.Name, stored.Domain, stored.Path)
		if cookie.MaxAge < 0 || (!stored.Expires.IsZero() && stored.Expires.Before(now)) {
			continue
		}
		j.cookies = append(j.cookies, stored)
	}
}

func (j *persistentJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()

	now := time.Now()
	var out []*http.Cookie
	filtered := j.cookies[:0]
	for _, cookie := range j.cookies {
		if !cookie.Expires.IsZero() && cookie.Expires.Before(now) {
			continue
		}
		filtered = append(filtered, cookie)
		if !cookieMatchesURL(cookie, u) {
			continue
		}
		out = append(out, &http.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Path:     cookie.Path,
			Domain:   cookie.Domain,
			Expires:  cookie.Expires,
			Secure:   cookie.Secure,
			HttpOnly: cookie.HTTPOnly,
		})
	}
	j.cookies = filtered
	return out
}

func (j *persistentJar) Snapshot() []storedCookie {
	j.mu.Lock()
	defer j.mu.Unlock()

	out := make([]storedCookie, len(j.cookies))
	copy(out, j.cookies)
	sort.Slice(out, func(i, k int) bool {
		if out[i].Domain == out[k].Domain {
			if out[i].Path == out[k].Path {
				return out[i].Name < out[k].Name
			}
			return out[i].Path < out[k].Path
		}
		return out[i].Domain < out[k].Domain
	})
	return out
}

func (j *persistentJar) deleteCookieLocked(name, domain, path string) {
	filtered := j.cookies[:0]
	for _, existing := range j.cookies {
		if existing.Name == name && existing.Domain == domain && existing.Path == path {
			continue
		}
		filtered = append(filtered, existing)
	}
	j.cookies = filtered
}

func cookieMatchesURL(cookie storedCookie, u *url.URL) bool {
	host := u.Hostname()
	if cookie.Domain != "" {
		domain := strings.TrimPrefix(cookie.Domain, ".")
		if host != domain && !strings.HasSuffix(host, "."+domain) {
			return false
		}
	}
	if cookie.Secure && u.Scheme != "https" {
		return false
	}
	path := cookie.Path
	if path == "" {
		path = "/"
	}
	return strings.HasPrefix(u.EscapedPath(), path)
}

func defaultCookiePath(path string) string {
	if path == "" || path[0] != '/' {
		return "/"
	}
	if path == "/" {
		return "/"
	}
	index := strings.LastIndex(path, "/")
	if index <= 0 {
		return "/"
	}
	return path[:index]
}

func loadState(dir, profileName string) (*profileState, error) {
	path := statePath(dir, profileName)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &profileState{Values: map[string]string{}}, nil
		}
		return nil, fmt.Errorf("read state file: %v", pathErrorCause(err))
	}

	var state profileState
	if err := json.Unmarshal(content, &state); err != nil {
		return nil, err
	}
	if state.Values == nil {
		state.Values = map[string]string{}
	}
	return &state, nil
}

func saveState(dir, profileName string, state *profileState) error {
	if state.Values == nil {
		state.Values = map[string]string{}
	}
	if err := ensureStateDirectory(dir); err != nil {
		return err
	}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')

	temp, err := os.CreateTemp(dir, "."+profileName+".json.tmp-")
	if err != nil {
		return fmt.Errorf("create temporary state file: %v", pathErrorCause(err))
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary state permissions: %v", pathErrorCause(err))
	}
	if _, err := temp.Write(content); err != nil {
		return fmt.Errorf("write temporary state file: %v", pathErrorCause(err))
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary state file: %v", pathErrorCause(err))
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary state file: %v", pathErrorCause(err))
	}
	if err := os.Rename(tempPath, statePath(dir, profileName)); err != nil {
		return fmt.Errorf("replace state file: %v", pathErrorCause(err))
	}
	removeTemp = false
	if err := syncStateDirectory(dir); err != nil {
		return fmt.Errorf("sync state directory: %v", pathErrorCause(err))
	}
	return nil
}

func statePath(dir, profileName string) string {
	return filepath.Join(dir, profileName+".json")
}

// Package auth contains the authentication system.
package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/httpp"
	"github.com/bluenviron/mediamtx/internal/protocols/tls"
)

const (
	maxInboundBodySize = 128 * 1024
	// applications add and remove their keys while running.
	jwksRefreshPeriod = 60 * time.Second
	jwksRetryPeriod   = 10 * time.Second
	jwksFetchTimeout  = 3 * time.Second
)

func isHTTP(req *Request) bool {
	return req.Protocol == ProtocolHLS || req.Protocol == ProtocolWebRTC ||
		req.Action == conf.AuthActionPlayback ||
		req.Action == conf.AuthActionAPI ||
		req.Action == conf.AuthActionMetrics ||
		req.Action == conf.AuthActionPprof
}

func matchesPermission(perms []conf.AuthInternalUserPermission, req *Request) bool {
	for _, perm := range perms {
		if perm.Action == req.Action {
			if perm.Action == conf.AuthActionPublish ||
				perm.Action == conf.AuthActionRead ||
				perm.Action == conf.AuthActionPlayback {
				switch {
				case perm.Path == "":
					return true

				case strings.HasPrefix(perm.Path, "~"):
					regexp, err := regexp.Compile(perm.Path[1:])
					if err == nil && regexp.MatchString(req.Path) {
						return true
					}

				case perm.Path == req.Path:
					return true
				}
			} else {
				return true
			}
		}
	}

	return false
}

// TokenKeyID returns the key a request's token says it was signed with. hasToken is false
// when the request carries no token at all. The token is not verified here:
// Authenticate() still does that, so a forged key ID only gets the request rejected later.
func TokenKeyID(req *Request, tokenInHTTPQuery bool) (keyID string, hasToken bool) {
	if req.Credentials == nil {
		return "", false
	}

	token := getToken(tokenInHTTPQuery, req)
	if token == "" {
		return "", false
	}

	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return "", true
	}

	keyID, _ = parsed.Header["kid"].(string)
	return keyID, true
}

func getToken(tokenInHTTPQuery bool, req *Request) string {
	switch {
	case req.Credentials.Token != "":
		return req.Credentials.Token

	case req.Credentials.Pass != "":
		return req.Credentials.Pass

		// always allow passing tokens through query parameters with RTSP and RTMP since there's no alternative.
	case req.Protocol == ProtocolRTSP || req.Protocol == ProtocolRTMP ||
		(tokenInHTTPQuery && isHTTP(req)):
		v, err := url.ParseQuery(req.Query)
		if err == nil {
			if len(v["token"]) == 1 {
				return v["token"][0]
			}

			// legacy query key
			if len(v["jwt"]) == 1 {
				return v["jwt"][0]
			}
		}
	}

	return ""
}

// Manager is the authentication manager.
type Manager struct {
	Method             conf.AuthMethod
	InternalUsers      []conf.AuthInternalUser
	HTTPAddress        string
	HTTPFingerprint    string
	HTTPExclude        []conf.AuthInternalUserPermission
	JWTJWKS            string
	JWTJWKSFingerprint string
	JWTClaimKey        string
	JWTExclude         []conf.AuthInternalUserPermission
	JWTInHTTPQuery     *bool
	JWTIssuer          string
	JWTAudience        string
	ReadTimeout        time.Duration
	Parent             logger.Writer

	wrapTransport   func(http.RoundTripper) http.RoundTripper
	mutex           sync.RWMutex
	jwksLastRefresh time.Time
	jwksRefreshing  bool
	jwksLastAttempt time.Time
	jwtKeyFunc      keyfunc.Keyfunc
}

// Initialize initializes the manager.
func (m *Manager) Initialize() {
	m.wrapTransport = func(tr http.RoundTripper) http.RoundTripper {
		return &httpp.LoggerTransport{Transport: tr, Log: m.Parent}
	}
}

// ReloadInternalUsers reloads InternalUsers.
func (m *Manager) ReloadInternalUsers(u []conf.AuthInternalUser) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.InternalUsers = u
}

// Authenticate authenticates a request.
func (m *Manager) Authenticate(req *Request) (*Result, *Error) {
	var token string
	if m.Method == conf.AuthMethodHTTP || m.Method == conf.AuthMethodJWT {
		token = getToken(m.Method == conf.AuthMethodJWT && m.JWTInHTTPQuery != nil && *m.JWTInHTTPQuery, req)
	}

	var res *Result
	var err error

	switch m.Method {
	case conf.AuthMethodInternal:
		res, err = m.authenticateInternal(req)

	case conf.AuthMethodHTTP:
		res, err = m.authenticateHTTP(req, token)

	default:
		res, err = m.authenticateJWT(req, token)
	}

	if err != nil {
		return nil, &Error{
			Wrapped:        err,
			AskCredentials: req.EnableAskCredentials && req.Credentials.User == "" && req.Credentials.Pass == "" && token == "",
		}
	}

	return res, nil
}

func (m *Manager) authenticateInternal(req *Request) (*Result, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for _, u := range m.InternalUsers {
		if ok := m.authenticateWithUser(req, &u); ok {
			return &Result{User: req.Credentials.User}, nil
		}
	}

	return nil, fmt.Errorf("authentication failed")
}

func (m *Manager) authenticateWithUser(
	req *Request,
	u *conf.AuthInternalUser,
) bool {
	if len(u.IPs) != 0 && !u.IPs.Contains(req.IP) {
		return false
	}

	if !matchesPermission(u.Permissions, req) {
		return false
	}

	if u.User != "any" {
		if req.CustomVerifyFunc != nil {
			if ok := req.CustomVerifyFunc(string(u.User), string(u.Pass)); !ok {
				return false
			}
		} else {
			if !u.User.Check(req.Credentials.User) || !u.Pass.Check(req.Credentials.Pass) {
				return false
			}
		}
	}

	return true
}

func (m *Manager) authenticateHTTP(req *Request, token string) (*Result, error) {
	if matchesPermission(m.HTTPExclude, req) {
		return &Result{User: req.Credentials.User}, nil
	}

	enc, _ := json.Marshal(struct {
		IP        string     `json:"ip"`
		User      string     `json:"user"`
		Password  string     `json:"password"`
		Token     string     `json:"token"`
		Action    string     `json:"action"`
		Path      string     `json:"path"`
		Protocol  string     `json:"protocol"`
		ID        *uuid.UUID `json:"id"`
		Query     string     `json:"query"`
		UserAgent string     `json:"userAgent"`
	}{
		IP:        req.IP.String(),
		User:      req.Credentials.User,
		Password:  req.Credentials.Pass,
		Token:     token,
		Action:    string(req.Action),
		Path:      req.Path,
		Protocol:  string(req.Protocol),
		ID:        req.ID,
		Query:     req.Query,
		UserAgent: req.UserAgent,
	})

	tr := &http.Transport{
		TLSClientConfig: tls.MakeConfig(m.HTTPFingerprint),
	}
	defer tr.CloseIdleConnections()

	httpClient := &http.Client{
		Timeout:   m.ReadTimeout,
		Transport: m.wrapTransport(tr),
	}

	httpRes, err := httpClient.Post(m.HTTPAddress, "application/json", bytes.NewReader(enc))
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpRes.Body.Close()

	if httpRes.StatusCode < 200 || httpRes.StatusCode > 299 {
		resBody, err2 := io.ReadAll(&customLimitReader{httpRes.Body, maxInboundBodySize})
		if err2 == nil && len(resBody) != 0 {
			return nil, fmt.Errorf("server replied with code %d: %s", httpRes.StatusCode, string(resBody))
		}

		return nil, fmt.Errorf("server replied with code %d", httpRes.StatusCode)
	}

	resBody, err := io.ReadAll(&customLimitReader{httpRes.Body, maxInboundBodySize})
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP response: %w", err)
	}

	var expireAt *time.Time

	if len(bytes.TrimSpace(resBody)) != 0 {
		var out struct {
			ExpireAt *time.Time `json:"expireAt"`
		}

		err = json.Unmarshal(resBody, &out)
		if err == nil {
			if out.ExpireAt != nil && !time.Now().Before(*out.ExpireAt) {
				return nil, fmt.Errorf("HTTP authentication expired")
			}
			expireAt = out.ExpireAt
		}
	}

	return &Result{User: req.Credentials.User, ExpiresAt: expireAt}, nil
}

func (m *Manager) authenticateJWT(req *Request, token string) (*Result, error) {
	if matchesPermission(m.JWTExclude, req) {
		return &Result{User: req.Credentials.User}, nil
	}

	if token == "" {
		return nil, fmt.Errorf("JWT not provided")
	}

	keyfunc, err := m.pullJWTJWKS()
	if err != nil {
		return nil, err
	}

	var opts []jwt.ParserOption
	if m.JWTIssuer != "" {
		opts = append(opts, jwt.WithIssuer(m.JWTIssuer))
	}
	if m.JWTAudience != "" {
		opts = append(opts, jwt.WithAudience(m.JWTAudience))
	}

	var cc jwtClaims
	cc.permissionsKey = m.JWTClaimKey
	_, err = jwt.ParseWithClaims(token, &cc, keyfunc, opts...)
	if err != nil {
		return nil, err
	}

	if !matchesPermission(cc.permissions, req) {
		return nil, fmt.Errorf("user doesn't have permission to perform action")
	}

	res := &Result{User: cc.Subject}
	if cc.ExpiresAt != nil {
		res.ExpiresAt = &cc.ExpiresAt.Time
	}

	return res, nil
}

// pullJWTJWKS only blocks on the first fetch. Authenticate() runs inside the path manager
// loop, so later refreshes happen in the background, and a JWKS server that is slow or
// down leaves the keys already fetched in place rather than stopping every reader.
func (m *Manager) pullJWTJWKS() (jwt.Keyfunc, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.jwtKeyFunc != nil {
		if time.Since(m.jwksLastRefresh) >= jwksRefreshPeriod && !m.jwksRefreshing {
			m.jwksRefreshing = true
			go m.refreshJWKSInBackground()
		}
		return m.jwtKeyFunc.Keyfunc, nil
	}

	// with no keys yet this does block, so a JWKS server that is down at boot is only
	// waited for once per retry period, not by every reader.
	if time.Since(m.jwksLastAttempt) < jwksRetryPeriod {
		return nil, fmt.Errorf("JWKS not available yet")
	}
	m.jwksLastAttempt = time.Now()

	tmp, err := m.fetchJWKS()
	if err != nil {
		return nil, err
	}

	m.jwtKeyFunc = tmp
	m.jwksLastRefresh = time.Now()

	return m.jwtKeyFunc.Keyfunc, nil
}

func (m *Manager) refreshJWKSInBackground() {
	tmp, err := m.fetchJWKS()

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.jwksRefreshing = false
	m.jwksLastRefresh = time.Now()

	if err != nil {
		m.Parent.Log(logger.Error, "unable to refresh JWKS, keeping the last keys: %v", err)
		return
	}

	m.jwtKeyFunc = tmp
}

func (m *Manager) fetchJWKS() (keyfunc.Keyfunc, error) {
	tr := &http.Transport{
		TLSClientConfig: tls.MakeConfig(m.JWTJWKSFingerprint),
	}
	defer tr.CloseIdleConnections()

	httpClient := &http.Client{
		Timeout:   jwksFetchTimeout,
		Transport: m.wrapTransport(tr),
	}

	res, err := httpClient.Get(m.JWTJWKS)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("JWKS server replied with code %d", res.StatusCode)
	}

	var raw json.RawMessage
	err = json.NewDecoder(&customLimitReader{res.Body, maxInboundBodySize}).Decode(&raw)
	if err != nil {
		return nil, err
	}

	return keyfunc.NewJWKSetJSON(raw)
}

// RefreshJWTJWKS refreshes the JWT JWKS. It is called by the control API, outside of the
// path manager loop, so it can wait for the result. The last keys stay on failure.
func (m *Manager) RefreshJWTJWKS() {
	tmp, err := m.fetchJWKS()

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.jwksLastAttempt = time.Time{}

	if err != nil {
		m.jwksLastRefresh = time.Time{}
		m.Parent.Log(logger.Error, "unable to refresh JWKS, keeping the last keys: %v", err)
		return
	}

	m.jwtKeyFunc = tmp
	m.jwksLastRefresh = time.Now()
}

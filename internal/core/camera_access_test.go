package core

import (
	"errors"
	"regexp"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/cameraaccess"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/test"
)

func TestPathManagerPublishBlocklist(t *testing.T) {
	s := miniredis.RunT(t)
	_, err := s.SAdd("camera:blocked_stream_ids", "BAD1")
	require.NoError(t, err)

	a := &cameraaccess.Access{
		RedisAddress: s.Addr(),
		BlockKey:     "camera:blocked_stream_ids",
		PathPrefix:   "live/",
		Parent:       test.NilLogger,
	}
	a.Initialize()
	defer a.Close()

	pm := &pathManager{
		authManager: test.NilAuthManager,
		pathConfs: map[string]*conf.Path{
			"all_others": {
				Regexp: regexp.MustCompile("^.*$"),
				Name:   "all_others",
				Source: "publisher",
			},
		},
		parent:           test.NilLogger,
		cameraAccess:     a,
		publishBlocklist: true,
	}
	pm.initialize()
	defer pm.close()

	addPublisher := func(name string) error {
		_, err := pm.AddPublisher(defs.PathAddPublisherReq{
			Author: &dummyPublisher{},
			Desc:   &description.Session{},
			AccessRequest: defs.PathAccessRequest{
				Name:    name,
				Publish: true,
			},
		})
		return err
	}

	findPathConf := func(name string, publish bool) error {
		_, err := pm.FindPathConf(defs.PathFindPathConfReq{
			Author: &dummyPublisher{},
			AccessRequest: defs.PathAccessRequest{
				Name:    name,
				Publish: publish,
			},
		})
		return err
	}

	require.NoError(t, addPublisher("live/CAM1"))
	require.NoError(t, findPathConf("live/CAM1", true))

	// both entry points must produce an authentication error, since that is
	// what makes the RTMP server send a proper rejection to the camera.
	_, ok := errors.AsType[*auth.Error](addPublisher("live/BAD1"))
	require.True(t, ok)
	_, ok = errors.AsType[*auth.Error](findPathConf("live/BAD1", true))
	require.True(t, ok)

	// cameras that are not blocked connect, registered or not.
	require.NoError(t, addPublisher("live/UNKNOWN"))

	// reading is not subject to the blocklist.
	require.NoError(t, findPathConf("live/BAD1", false))
}

type fakeOwnerCheck struct {
	allowed bool
	keys    []string
}

func (f *fakeOwnerCheck) Check(string) error {
	return nil
}

func (f *fakeOwnerCheck) CheckViewToken(keyID string, _ string) error {
	f.keys = append(f.keys, keyID)
	if !f.allowed {
		return errors.New("not owned")
	}
	return nil
}

func TestPathManagerViewTokenOwner(t *testing.T) {
	signed := func(kid string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{})
		token.Header["kid"] = kid
		res, err := token.SignedString([]byte("key"))
		require.NoError(t, err)
		return res
	}

	run := func(check *fakeOwnerCheck, req defs.PathAccessRequest) error {
		pm := &pathManager{
			authManager: test.NilAuthManager,
			pathConfs: map[string]*conf.Path{
				"all_others": {Regexp: regexp.MustCompile("^.*$"), Name: "all_others", Source: "publisher"},
			},
			parent:              test.NilLogger,
			cameraAccess:        check,
			viewTokenOwnerCheck: true,
			jwtInHTTPQuery:      true,
		}
		pm.initialize()
		defer pm.close()

		_, err := pm.FindPathConf(defs.PathFindPathConfReq{Author: &dummyPublisher{}, AccessRequest: req})
		return err
	}

	check := &fakeOwnerCheck{}
	err := run(check, defs.PathAccessRequest{
		Name:        "live/CAM1",
		Proto:       auth.ProtocolHLS,
		Query:       "token=" + signed("one"),
		Credentials: &auth.Credentials{},
	})
	_, ok := errors.AsType[*auth.Error](err)
	require.True(t, ok)
	require.Equal(t, []string{"one"}, check.keys)

	check = &fakeOwnerCheck{allowed: true}
	require.NoError(t, run(check, defs.PathAccessRequest{
		Name:        "live/CAM1",
		Credentials: &auth.Credentials{Token: signed("one")},
	}))

	// no key ID: the verifier would try every key, so it must never reach it.
	check = &fakeOwnerCheck{allowed: true}
	noKid, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{}).SignedString([]byte("key"))
	require.NoError(t, err)
	err = run(check, defs.PathAccessRequest{Name: "live/CAM1", Credentials: &auth.Credentials{Token: noKid}})
	_, ok = errors.AsType[*auth.Error](err)
	require.True(t, ok)
	require.Empty(t, check.keys)

	// a request without credentials must not crash the check.
	require.NoError(t, run(&fakeOwnerCheck{}, defs.PathAccessRequest{Name: "live/CAM1"}))

	// no token: left to normal authentication.
	check = &fakeOwnerCheck{}
	require.NoError(t, run(check, defs.PathAccessRequest{Name: "live/CAM1", Credentials: &auth.Credentials{}}))
	require.Empty(t, check.keys)

	// publishers are covered by the blocklist instead.
	check = &fakeOwnerCheck{}
	require.NoError(t, run(check, defs.PathAccessRequest{
		Name:        "live/CAM1",
		Publish:     true,
		Query:       "token=" + signed("one"),
		Credentials: &auth.Credentials{},
	}))
	require.Empty(t, check.keys)
}

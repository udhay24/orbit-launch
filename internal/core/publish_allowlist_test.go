package core

import (
	"errors"
	"regexp"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/publishallowlist"
	"github.com/bluenviron/mediamtx/internal/test"
)

func TestPathManagerPublishAllowlist(t *testing.T) {
	s := miniredis.RunT(t)
	_, err := s.SAdd("camera:stream_ids", "CAM1")
	require.NoError(t, err)

	a := &publishallowlist.Allowlist{
		RedisAddress: s.Addr(),
		Key:          "camera:stream_ids",
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
		publishAllowlist: a,
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
	_, ok := errors.AsType[*auth.Error](addPublisher("live/UNKNOWN"))
	require.True(t, ok)
	_, ok = errors.AsType[*auth.Error](findPathConf("live/UNKNOWN", true))
	require.True(t, ok)

	// reading is not subject to the allowlist.
	require.NoError(t, findPathConf("live/UNKNOWN", false))
}

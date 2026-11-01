package core

import (
	"regexp"
	"testing"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/test"
)

// TestPathManagerPublisherLimit exercises the fork's maxPublishers /
// publisherHysteresis gate (internal/core/path_manager.go). It is a sequential
// scenario rather than a table because the limit is a stateful machine: each
// step depends on the activePublishers count left by the previous one.
//
// All transitions are routed through the single pathManager.run() goroutine
// (AddPublisher -> path ready -> doSetPathReady++, setPathNotReady -> doSetPathNotReady--),
// so ordering is guaranteed without sleeps: a later AddPublisher cannot be
// processed until the prior ready/not-ready has been applied.
func TestPathManagerPublisherLimit(t *testing.T) {
	pathConfs := map[string]*conf.Path{
		"all_others": {
			Regexp: regexp.MustCompile("^.*$"),
			Name:   "all_others",
			Source: "publisher",
		},
	}

	pm := &pathManager{
		authManager:         test.NilAuthManager,
		pathConfs:           pathConfs,
		parent:              test.NilLogger,
		maxPublishers:       3,
		publisherHysteresis: 1,
	}
	pm.initialize()
	defer pm.close()

	// publish creates a path and makes it ready, incrementing activePublishers.
	// On rejection it returns the error and a nil path.
	publish := func(name string) (*path, error) {
		res, err := pm.AddPublisher(defs.PathAddPublisherReq{
			Author:        &dummyPublisher{},
			Desc:          &description.Session{},
			AccessRequest: defs.PathAccessRequest{Name: name},
		})
		if err != nil {
			return nil, err
		}
		return res.Path.(*path), nil
	}

	// Fill up to the cap (3).
	p1, err := publish("cam1")
	require.NoError(t, err)
	p2, err := publish("cam2")
	require.NoError(t, err)
	_, err = publish("cam3")
	require.NoError(t, err)

	// Cap reached -> further publishers are rejected.
	_, err = publish("cam4")
	require.EqualError(t, err, "maximum publisher count reached")

	// Drop one (3 -> 2). With hysteresis 1 the limit clears only below
	// maxPublishers-hysteresis (2), so at exactly 2 it must still hold.
	pm.setPathNotReady(p1)
	_, err = publish("cam5")
	require.EqualError(t, err, "maximum publisher count reached")

	// Drop another (2 -> 1). Now below the hysteresis threshold -> limit clears.
	pm.setPathNotReady(p2)
	_, err = publish("cam6")
	require.NoError(t, err)
}

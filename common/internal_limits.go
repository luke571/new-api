package common

import (
	"errors"
	"os"
	"strconv"
	"sync"
	"time"
)

type internalRequestWindow struct {
	start  time.Time
	count  int
	active bool
}

var internalRequestLimits = struct {
	sync.Mutex
	users  map[int]internalRequestWindow
	active int
}{users: make(map[int]internalRequestWindow)}

// AcquireInternalRequest limits every key belonging to one user in this single
// process. It does not impose a deadline on an established upstream connection.
func AcquireInternalRequest(userID int) (func(), error) {
	return acquireInternalRequestAt(userID, time.Now())
}
func acquireInternalRequestAt(userID int, now time.Time) (func(), error) {
	if userID <= 0 {
		return nil, errors.New("invalid request owner")
	}
	internalRequestLimits.Lock()
	state := internalRequestLimits.users[userID]
	capacity, parseErr := strconv.Atoi(os.Getenv("INTERNAL_UPSTREAM_CONCURRENCY"))
	if parseErr != nil || capacity < 1 {
		capacity = 2
	}
	if internalRequestLimits.active >= capacity {
		internalRequestLimits.Unlock()
		return nil, errors.New("upstream concurrency capacity reached; retry later")
	}
	if state.active {
		internalRequestLimits.Unlock()
		return nil, errors.New("one concurrent request per user is allowed")
	}
	if state.start.IsZero() || now.Sub(state.start) >= time.Minute {
		state.start = now
		state.count = 0
	}
	if state.count >= 40 {
		internalRequestLimits.Unlock()
		return nil, errors.New("request limit reached: 40 requests per minute")
	}
	state.count++
	state.active = true
	internalRequestLimits.active++
	internalRequestLimits.users[userID] = state
	// Retain only a bounded idle history; active streams are never evicted.
	if len(internalRequestLimits.users) > 10000 {
		for id, item := range internalRequestLimits.users {
			if !item.active && now.Sub(item.start) >= time.Minute {
				delete(internalRequestLimits.users, id)
			}
		}
	}
	internalRequestLimits.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			internalRequestLimits.Lock()
			item := internalRequestLimits.users[userID]
			item.active = false
			internalRequestLimits.active--
			internalRequestLimits.users[userID] = item
			internalRequestLimits.Unlock()
		})
	}, nil
}

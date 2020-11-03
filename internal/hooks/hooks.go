// Copyright 2020 The Matrix.org Foundation C.I.C.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package hooks exposes places in Dendrite where custom code can be executed, useful for MSCs.
// Hooks can only be run in monolith mode.
package hooks

import "sync"

const (
	// KindNewEvent is a hook which is called with *gomatrixserverlib.HeaderedEvent
	// It is run when a new event is persisted in the roomserver.
	// Usage:
	//   hooks.Attach(hooks.KindNewEvent, func(headeredEvent interface{}) { ... })
	KindNewEvent = "new_event"
)

var (
	hookMap = make(map[string][]func(interface{}))
	hookMu  = sync.Mutex{}
	enabled = false
)

// Enable all hooks. This may slow down the server slightly. Required for MSCs to work.
func Enable() {
	enabled = true
}

// Run any hooks
func Run(kind string, data interface{}) {
	if !enabled {
		return
	}
	cbs := callbacks(kind)
	for _, cb := range cbs {
		cb(data)
	}
}

// Attach a hook
func Attach(kind string, callback func(interface{})) {
	if !enabled {
		return
	}
	hookMu.Lock()
	defer hookMu.Unlock()
	hookMap[kind] = append(hookMap[kind], callback)
}

func callbacks(kind string) []func(interface{}) {
	hookMu.Lock()
	defer hookMu.Unlock()
	return hookMap[kind]
}
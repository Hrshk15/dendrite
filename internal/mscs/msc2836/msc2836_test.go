package msc2836_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	nethttputil "net/http/httputil"

	"github.com/gorilla/mux"
	"github.com/matrix-org/dendrite/internal/config"
	"github.com/matrix-org/dendrite/internal/hooks"
	"github.com/matrix-org/dendrite/internal/httputil"
	"github.com/matrix-org/dendrite/internal/mscs/msc2836"
	"github.com/matrix-org/dendrite/internal/setup"
	roomserver "github.com/matrix-org/dendrite/roomserver/api"
	userapi "github.com/matrix-org/dendrite/userapi/api"
	"github.com/matrix-org/gomatrixserverlib"
)

var (
	client = &http.Client{
		Timeout: 10 * time.Second,
	}
	constTrue  = true
	constFalse = false
)

// Basic sanity check of MSC2836 logic. Injects a thread that looks like:
//   A
//   |
//   B
//  / \
// C   D
//    /|\
//   E F G
//   |
//   H
// And makes sure POST /relationships works with various parameters
func TestMSC2836(t *testing.T) {
	alice := "@alice:localhost"
	bob := "@bob:localhost"
	charlie := "@charlie:localhost"
	roomIDA := "!alice:localhost"
	roomIDB := "!bob:localhost"
	roomIDC := "!charlie:localhost"
	// give access tokens to all three users
	nopUserAPI := &testUserAPI{
		accessTokens: make(map[string]userapi.Device),
	}
	nopUserAPI.accessTokens["alice"] = userapi.Device{
		AccessToken: "alice",
		DisplayName: "Alice",
		UserID:      alice,
	}
	nopUserAPI.accessTokens["bob"] = userapi.Device{
		AccessToken: "bob",
		DisplayName: "Bob",
		UserID:      bob,
	}
	nopUserAPI.accessTokens["charlie"] = userapi.Device{
		AccessToken: "charlie",
		DisplayName: "Charles",
		UserID:      charlie,
	}
	eventA := mustCreateEvent(t, gomatrixserverlib.RoomVersionV6, fledglingEvent{
		RoomID: roomIDA,
		Sender: alice,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "[A] Do you know shelties?",
		},
	})
	eventB := mustCreateEvent(t, gomatrixserverlib.RoomVersionV6, fledglingEvent{
		RoomID: roomIDB,
		Sender: bob,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "[B] I <3 shelties",
			"m.relationship": map[string]string{
				"rel_type": "m.reference",
				"event_id": eventA.EventID(),
			},
		},
	})
	eventC := mustCreateEvent(t, gomatrixserverlib.RoomVersionV6, fledglingEvent{
		RoomID: roomIDB,
		Sender: bob,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "[C] like so much",
			"m.relationship": map[string]string{
				"rel_type": "m.reference",
				"event_id": eventB.EventID(),
			},
		},
	})
	eventD := mustCreateEvent(t, gomatrixserverlib.RoomVersionV6, fledglingEvent{
		RoomID: roomIDA,
		Sender: alice,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "[D] but what are shelties???",
			"m.relationship": map[string]string{
				"rel_type": "m.reference",
				"event_id": eventB.EventID(),
			},
		},
	})
	eventE := mustCreateEvent(t, gomatrixserverlib.RoomVersionV6, fledglingEvent{
		RoomID: roomIDB,
		Sender: bob,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "[E] seriously???",
			"m.relationship": map[string]string{
				"rel_type": "m.reference",
				"event_id": eventD.EventID(),
			},
		},
	})
	eventF := mustCreateEvent(t, gomatrixserverlib.RoomVersionV6, fledglingEvent{
		RoomID: roomIDC,
		Sender: charlie,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "[F] omg how do you not know what shelties are",
			"m.relationship": map[string]string{
				"rel_type": "m.reference",
				"event_id": eventD.EventID(),
			},
		},
	})
	eventG := mustCreateEvent(t, gomatrixserverlib.RoomVersionV6, fledglingEvent{
		RoomID: roomIDA,
		Sender: alice,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "[G] looked it up, it's a sheltered person?",
			"m.relationship": map[string]string{
				"rel_type": "m.reference",
				"event_id": eventD.EventID(),
			},
		},
	})
	eventH := mustCreateEvent(t, gomatrixserverlib.RoomVersionV6, fledglingEvent{
		RoomID: roomIDB,
		Sender: bob,
		Type:   "m.room.message",
		Content: map[string]interface{}{
			"body": "[H] it's a dog!!!!!",
			"m.relationship": map[string]string{
				"rel_type": "m.reference",
				"event_id": eventE.EventID(),
			},
		},
	})
	// make everyone joined to each other's rooms
	nopRsAPI := &testRoomserverAPI{
		userToJoinedRooms: map[string][]string{
			alice:   []string{roomIDA, roomIDB, roomIDC},
			bob:     []string{roomIDA, roomIDB, roomIDC},
			charlie: []string{roomIDA, roomIDB, roomIDC},
		},
		events: map[string]*gomatrixserverlib.HeaderedEvent{
			eventA.EventID(): eventA,
			eventB.EventID(): eventB,
			eventC.EventID(): eventC,
			eventD.EventID(): eventD,
			eventE.EventID(): eventE,
			eventF.EventID(): eventF,
			eventG.EventID(): eventG,
			eventH.EventID(): eventH,
		},
	}
	router := injectEvents(t, nopUserAPI, nopRsAPI, []*gomatrixserverlib.HeaderedEvent{
		eventA, eventB, eventC, eventD, eventE, eventF, eventG, eventH,
	})
	cancel := runServer(t, router)
	defer cancel()

	t.Run("returns 403 on invalid event IDs", func(t *testing.T) {
		res := postRelationships(t, "alice", &msc2836.EventRelationshipRequest{
			EventID: "$invalid",
		})
		if res.StatusCode != 403 {
			out, _ := nethttputil.DumpResponse(res, true)
			t.Fatalf("failed to perform request: %s", string(out))
		}
	})
	t.Run("returns the parent if include_parent is true", func(t *testing.T) {
		res := postRelationships(t, "alice", &msc2836.EventRelationshipRequest{
			EventID:       eventB.EventID(),
			IncludeParent: &constTrue,
			Limit:         1,
		})
		if res.StatusCode != 200 {
			out, _ := nethttputil.DumpResponse(res, true)
			t.Fatalf("failed to perform request: %s", string(out))
		}
	})
}

func runServer(t *testing.T, router *mux.Router) func() {
	t.Helper()
	externalServ := &http.Server{
		Addr:         string(":8009"),
		WriteTimeout: 60 * time.Second,
		Handler:      router,
	}
	go func() {
		err := externalServ.ListenAndServe()
		if err != nil {
			t.Logf("ListenAndServe: %s", err)
		}
	}()
	// wait to listen on the port
	time.Sleep(500 * time.Millisecond)
	return func() {
		externalServ.Shutdown(context.TODO())
	}
}

func postRelationships(t *testing.T, accessToken string, req *msc2836.EventRelationshipRequest) *http.Response {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %s", err)
	}
	httpReq, err := http.NewRequest(
		"POST", "http://localhost:8009/_matrix/client/unstable/event_relationships",
		bytes.NewBuffer(data),
	)
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	if err != nil {
		t.Fatalf("failed to prepare request: %s", err)
	}
	res, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("failed to do request: %s", err)
	}
	return res
}

type testUserAPI struct {
	accessTokens map[string]userapi.Device
}

func (u *testUserAPI) InputAccountData(ctx context.Context, req *userapi.InputAccountDataRequest, res *userapi.InputAccountDataResponse) error {
	return nil
}
func (u *testUserAPI) PerformAccountCreation(ctx context.Context, req *userapi.PerformAccountCreationRequest, res *userapi.PerformAccountCreationResponse) error {
	return nil
}
func (u *testUserAPI) PerformPasswordUpdate(ctx context.Context, req *userapi.PerformPasswordUpdateRequest, res *userapi.PerformPasswordUpdateResponse) error {
	return nil
}
func (u *testUserAPI) PerformDeviceCreation(ctx context.Context, req *userapi.PerformDeviceCreationRequest, res *userapi.PerformDeviceCreationResponse) error {
	return nil
}
func (u *testUserAPI) PerformDeviceDeletion(ctx context.Context, req *userapi.PerformDeviceDeletionRequest, res *userapi.PerformDeviceDeletionResponse) error {
	return nil
}
func (u *testUserAPI) PerformDeviceUpdate(ctx context.Context, req *userapi.PerformDeviceUpdateRequest, res *userapi.PerformDeviceUpdateResponse) error {
	return nil
}
func (u *testUserAPI) PerformAccountDeactivation(ctx context.Context, req *userapi.PerformAccountDeactivationRequest, res *userapi.PerformAccountDeactivationResponse) error {
	return nil
}
func (u *testUserAPI) QueryProfile(ctx context.Context, req *userapi.QueryProfileRequest, res *userapi.QueryProfileResponse) error {
	return nil
}
func (u *testUserAPI) QueryAccessToken(ctx context.Context, req *userapi.QueryAccessTokenRequest, res *userapi.QueryAccessTokenResponse) error {
	dev, ok := u.accessTokens[req.AccessToken]
	if !ok {
		res.Err = fmt.Errorf("unknown token")
		return nil
	}
	res.Device = &dev
	return nil
}
func (u *testUserAPI) QueryDevices(ctx context.Context, req *userapi.QueryDevicesRequest, res *userapi.QueryDevicesResponse) error {
	return nil
}
func (u *testUserAPI) QueryAccountData(ctx context.Context, req *userapi.QueryAccountDataRequest, res *userapi.QueryAccountDataResponse) error {
	return nil
}
func (u *testUserAPI) QueryDeviceInfos(ctx context.Context, req *userapi.QueryDeviceInfosRequest, res *userapi.QueryDeviceInfosResponse) error {
	return nil
}
func (u *testUserAPI) QuerySearchProfiles(ctx context.Context, req *userapi.QuerySearchProfilesRequest, res *userapi.QuerySearchProfilesResponse) error {
	return nil
}

type testRoomserverAPI struct {
	// use a trace API as it implements method stubs so we don't need to have them here.
	// We'll override the functions we care about.
	roomserver.RoomserverInternalAPITrace
	userToJoinedRooms map[string][]string
	events            map[string]*gomatrixserverlib.HeaderedEvent
}

func (r *testRoomserverAPI) QueryEventsByID(ctx context.Context, req *roomserver.QueryEventsByIDRequest, res *roomserver.QueryEventsByIDResponse) error {
	for _, eventID := range req.EventIDs {
		ev := r.events[eventID]
		if ev != nil {
			res.Events = append(res.Events, *ev)
		}
	}
	return nil
}

func (r *testRoomserverAPI) QueryMembershipForUser(ctx context.Context, req *roomserver.QueryMembershipForUserRequest, res *roomserver.QueryMembershipForUserResponse) error {
	rooms := r.userToJoinedRooms[req.UserID]
	for _, roomID := range rooms {
		if roomID == req.RoomID {
			res.IsInRoom = true
			res.HasBeenInRoom = true
			res.Membership = "join"
			break
		}
	}
	return nil
}

func injectEvents(t *testing.T, userAPI userapi.UserInternalAPI, rsAPI roomserver.RoomserverInternalAPI, events []*gomatrixserverlib.HeaderedEvent) *mux.Router {
	t.Helper()
	cfg := &config.Dendrite{}
	cfg.Defaults()
	cfg.Global.ServerName = "localhost"
	cfg.MSCs.Database.ConnectionString = "file:msc2836_test.db"
	cfg.MSCs.MSCs = []string{"msc2836"}
	base := &setup.BaseDendrite{
		Cfg:                cfg,
		PublicClientAPIMux: mux.NewRouter().PathPrefix(httputil.PublicClientPathPrefix).Subrouter(),
	}

	err := msc2836.Enable(base, rsAPI, userAPI)
	if err != nil {
		t.Fatalf("failed to enable MSC2836: %s", err)
	}
	for _, ev := range events {
		hooks.Run(hooks.KindNewEvent, ev)
	}
	return base.PublicClientAPIMux
}

type fledglingEvent struct {
	Type     string
	StateKey *string
	Content  interface{}
	Sender   string
	RoomID   string
}

func mustCreateEvent(t *testing.T, roomVer gomatrixserverlib.RoomVersion, ev fledglingEvent) (result *gomatrixserverlib.HeaderedEvent) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize) // zero seed
	key := ed25519.NewKeyFromSeed(seed)
	eb := gomatrixserverlib.EventBuilder{
		Sender:   ev.Sender,
		Depth:    999,
		Type:     ev.Type,
		StateKey: ev.StateKey,
		RoomID:   ev.RoomID,
	}
	err := eb.SetContent(ev.Content)
	if err != nil {
		t.Fatalf("mustCreateEvent: failed to marshal event content %+v", ev.Content)
	}
	signedEvent, err := eb.Build(time.Now(), gomatrixserverlib.ServerName("localhost"), "ed25519:test", key, roomVer)
	if err != nil {
		t.Fatalf("mustCreateEvent: failed to sign event: %s", err)
	}
	h := signedEvent.Headered(roomVer)
	return &h
}

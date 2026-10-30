package launcherhub

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAntiCheatPolicyBoundaries(t *testing.T) {
	good := &AntiCheatPolicy{}
	if good.validate() != nil || good.Mode != "audit" || good.Enabled {
		t.Fatal("default must be disabled audit")
	}
	bad := []AntiCheatPolicy{
		{Mode: "kill"}, {RequiredFiles: []AntiCheatFile{{Name: "../bad.dll", Sha256: strings.Repeat("a", 64)}}},
		{RequiredFiles: []AntiCheatFile{{Name: "CON.dll", Sha256: strings.Repeat("a", 64)}}},
		{Rules: []AntiCheatRule{{Id: "x", Scope: "process", Sha256: "filename.exe"}}},
		{CodeRegions: []AntiCheatRegion{{Id: "x", ExeSha256: strings.Repeat("a", 64), Rva: 0x7fffffff, Length: 32, Sha256: strings.Repeat("b", 64)}}},
	}
	for _, p := range bad {
		if p.validate() == nil {
			t.Fatalf("invalid policy accepted: %+v", p)
		}
	}
	raw, _ := json.Marshal(good)
	if bytes.Contains(raw, []byte("null")) {
		t.Fatal("absent arrays must not override client defaults with null")
	}
}
func TestAntiCheatSignedPublicationAndFlavorIsolation(t *testing.T) {
	h, _ := fixture(t)
	cookie, csrf := auth(t, h, true)
	h.profile.AntiCheat = &AntiCheatPolicy{Enabled: true, Mode: "audit"}
	makeBundle := func(flavor string) []byte {
		var out bytes.Buffer
		z := zip.NewWriter(&out)
		for _, name := range []string{"DNF90.exe", "DNF90.Core.exe", "flutter_windows.dll", "data/app.so", "data/icudtl.dat"} {
			w, _ := z.Create(name)
			w.Write([]byte("isolated fixture"))
		}
		w, _ := z.Create("build-flavor.json")
		w.Write([]byte(`{"format":1,"flavor":"` + flavor + `"}`))
		z.Close()
		return out.Bytes()
	}
	upload := func(version, flavor string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://admin.example.test/admin/api/bundle/"+version, bytes.NewReader(makeBundle(flavor)))
		r.AddCookie(cookie)
		r.Header.Set("Origin", "https://admin.example.test")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", "application/zip")
		w := httptest.NewRecorder()
		h.AdminHandler().ServeHTTP(w, r)
		return w
	}
	if w := upload("1.0.4", "standard"); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := upload("1.0.4", "anticheat"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if h.state.Published.Launcher.BuildFlavor != "anticheat" || h.state.Published.Profile.AntiCheat == nil || !h.state.Published.Profile.AntiCheat.Enabled {
		t.Fatal("policy/flavor not included in signed publication")
	}
	seq := h.state.Published.Sequence
	h.profile.AntiCheat.Enabled = false
	if !h.state.Published.Profile.AntiCheat.Enabled {
		t.Fatal("published policy aliases mutable configuration")
	}
	if w := upload("1.0.5", "standard"); w.Code != 409 || h.state.Published.Sequence != seq {
		t.Fatal("cross-flavor replacement was accepted")
	}
	restarted, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.state.Published.Launcher.BuildFlavor != "anticheat" {
		t.Fatal("flavor not durable")
	}
}

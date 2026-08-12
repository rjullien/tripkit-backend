package handlers_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type clientStore struct {
	checks  map[string]map[string]any
	custom  map[string]map[string]any
	deleted map[string]int64
	hidden  []string
	device  string
	now     int64
}

func newClient(dev string) *clientStore {
	return &clientStore{checks: map[string]map[string]any{}, custom: map[string]map[string]any{}, deleted: map[string]int64{}, hidden: []string{}, device: dev, now: 1_700_000_000_000}
}
func (c *clientStore) tick() int64 { c.now += 1000; return c.now }

func (c *clientStore) toggle(itemID string) {
	checked := false
	if cur, ok := c.checks[itemID]; ok {
		checked = cur["checked"].(bool)
	}
	c.checks[itemID] = map[string]any{"checked": !checked, "updatedAt": c.tick()}
}
func (c *clientStore) addCustom(id string, section int, text string) {
	c.custom[id] = map[string]any{"text": text, "section": section, "createdAt": c.tick(), "shared": true}
}
func (c *clientStore) applyCheck(itemID string, checked bool, updatedAt int64) {
	cur, ok := c.checks[itemID]
	if ok && cur["checked"].(bool) && !checked {
		if c.now-cur["updatedAt"].(int64) < 10000 {
			return
		}
	}
	if !ok || updatedAt >= cur["updatedAt"].(int64) {
		c.checks[itemID] = map[string]any{"checked": checked, "updatedAt": updatedAt}
	}
}
func (c *clientStore) syncBody() map[string]any {
	return map[string]any{"deviceId": c.device, "lastSyncAt": int64(0), "checks": c.checks, "custom": c.custom, "deletedCustom": c.deleted, "hidden": c.hidden}
}
func (c *clientStore) applyMerged(resp map[string]any) {
	merged, _ := resp["merged"].(map[string]any)
	if merged == nil {
		return
	}
	if mc, ok := merged["checks"].(map[string]any); ok {
		for id, v := range mc {
			st := v.(map[string]any)
			c.applyCheck(id, st["checked"].(bool), int64(st["updatedAt"].(float64)))
		}
	}
	if cu, ok := merged["custom"].(map[string]any); ok {
		for id, v := range cu {
			if _, tomb := c.deleted[id]; tomb {
				continue // ne pas ressusciter un item supprimé localement
			}
			if _, exists := c.custom[id]; !exists {
				it := v.(map[string]any)
				c.custom[id] = map[string]any{"text": it["text"], "section": it["section"], "createdAt": it["createdAt"], "shared": true}
			}
		}
	}
}
func (c *clientStore) isChecked(itemID string) bool {
	if v, ok := c.checks[itemID]; ok {
		return v["checked"].(bool)
	}
	return false
}
func (c *clientStore) hasCustom(id string) bool { _, ok := c.custom[id]; return ok }

func bootstrap(t *testing.T) *chi.Mux {
	t.Helper()
	r := setupRouter(t)
	if w := doReqAs(r, "POST", "/api/trips", map[string]any{"id": "malte-2026", "name": "Malte"}, "rene"); w.Code != 201 {
		t.Fatalf("create trip: %d %s", w.Code, w.Body.String())
	}
	if w := doReqAs(r, "PUT", "/api/trips/malte-2026/lists/checklist-malte",
		map[string]any{"type": "packing", "title": "Checklist", "owner_user": ""}, "rene"); w.Code != 200 {
		t.Fatalf("create list: %d %s", w.Code, w.Body.String())
	}
	return r
}
func sync(t *testing.T, r *chi.Mux, c *clientStore) map[string]any {
	t.Helper()
	w := doReqAs(r, "PATCH", "/api/trips/malte-2026/lists/checklist-malte/sync", c.syncBody(), "rene")
	if w.Code != 200 {
		t.Fatalf("sync failed: %d %s", w.Code, w.Body.String())
	}
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	c.applyMerged(m)
	return m
}
func serverChecked(m map[string]any, itemID string) (bool, bool) {
	merged, _ := m["merged"].(map[string]any)
	mc, _ := merged["checks"].(map[string]any)
	if v, ok := mc[itemID]; ok {
		return v.(map[string]any)["checked"].(bool), true
	}
	return false, false
}
func serverHasCustom(m map[string]any, id string) bool {
	merged, _ := m["merged"].(map[string]any)
	cu, _ := merged["custom"].(map[string]any)
	_, ok := cu[id]
	return ok
}

// Scénario A : je coche "prise UK" (div-1), puis un autre item (div-2).
func TestReproA_UncheckOnNextCheck(t *testing.T) {
	r := bootstrap(t)
	c := newClient("phone-rene")

	c.toggle("div-1") // prise UK
	sync(t, r, c)
	if !c.isChecked("div-1") {
		t.Fatal("après 1er sync, prise UK devrait être cochée localement")
	}

	c.toggle("div-2") // un autre item
	m := sync(t, r, c)

	if !c.isChecked("div-1") {
		t.Errorf("BUG REPRODUIT (local): prise UK décochée après avoir coché un autre item")
	}
	if v, ok := serverChecked(m, "div-1"); !ok || !v {
		t.Errorf("BUG REPRODUIT (serveur): prise UK = %v (présent=%v)", v, ok)
	}
}

// Scénario B : j'ajoute un item custom, je push, puis je fais une autre action.
func TestReproB_CustomDisappears(t *testing.T) {
	r := bootstrap(t)
	c := newClient("phone-rene")

	c.addCustom("c-cable", 0, "Câble USB-C extra")
	m := sync(t, r, c)
	if !serverHasCustom(m, "c-cable") {
		t.Errorf("BUG REPRODUIT: item custom absent du serveur juste après push")
	}
	if !c.hasCustom("c-cable") {
		t.Errorf("BUG REPRODUIT: item custom disparu en local juste après push")
	}

	c.toggle("div-3")
	m = sync(t, r, c)
	if !c.hasCustom("c-cable") {
		t.Errorf("BUG REPRODUIT: item custom disparu en local après action suivante")
	}
	if !serverHasCustom(m, "c-cable") {
		t.Errorf("BUG REPRODUIT: item custom disparu du serveur après action suivante")
	}
}

// Scénario C : 2e device avec snapshot périmé (Léa / autre onglet) qui resync.
func TestReproC_StaleSecondDevice(t *testing.T) {
	r := bootstrap(t)
	a := newClient("phone-rene")
	b := newClient("laptop-lea")

	a.toggle("div-1") // René coche prise UK
	sync(t, r, a)

	// Léa n'a jamais vu div-1 ; elle coche div-2 et resync avec SON état local
	b.toggle("div-2")
	mb := sync(t, r, b)

	// René refait une action plus tard
	a.toggle("div-4")
	ma := sync(t, r, a)

	if v, ok := serverChecked(ma, "div-1"); !ok || !v {
		t.Errorf("BUG: prise UK perdue côté serveur après resync device périmé (div-1=%v ok=%v)", v, ok)
	}
	_ = mb
}

// deleteCustom (store.js corrigé) : supprime localement + pose une tombstone
func (c *clientStore) deleteCustom(id string) {
	delete(c.custom, id)
	delete(c.checks, "custom-"+id)
	c.deleted[id] = c.tick()
}

// Scénario D : je supprime un item custom puis je resync → il revient (résurrection).
func TestReproD_DeletedCustomResurrects(t *testing.T) {
	r := bootstrap(t)
	c := newClient("phone-rene")

	c.addCustom("c-cable", 0, "Câble USB-C extra")
	sync(t, r, c) // serveur connaît maintenant c-cable

	c.deleteCustom("c-cable")
	if c.hasCustom("c-cable") {
		t.Fatal("devrait être supprimé localement")
	}

	m := sync(t, r, c) // resync : le serveur applique la tombstone et ne renvoie plus l'item

	if serverHasCustom(m, "c-cable") {
		t.Errorf("BUG: serveur renvoie encore c-cable après suppression (tombstone non appliquée)")
	}
	if c.hasCustom("c-cable") {
		t.Errorf("BUG: item custom supprimé est RESSUSCITÉ après resync")
	}

	// PWA resends deletedCustom on every later poll (12s). Must stay 200.
	m = sync(t, r, c)
	if serverHasCustom(m, "c-cable") {
		t.Errorf("BUG: c-cable reappeared on second tombstone sync")
	}
}

func TestHandlers_NoSQLiteOnlyMAXOnTombstones(t *testing.T) {
	src, err := os.ReadFile("handlers.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "MAX(list_custom_deleteds") {
		t.Fatal("SQLite-only MAX() in tombstone upsert — Postgres returns 500 on every later sync")
	}
}

// Scénario F : un device périmé qui a encore l'item en local ne doit pas le ressusciter.
func TestReproF_StaleDeviceCannotResurrect(t *testing.T) {
	r := bootstrap(t)
	a := newClient("phone-rene")
	b := newClient("laptop-lea")

	a.addCustom("c-cable", 0, "Câble USB-C")
	sync(t, r, a)
	// b récupère l'item via un sync
	mb := sync(t, r, b)
	if !b.hasCustom("c-cable") {
		t.Fatalf("device b devrait avoir reçu c-cable (%v)", mb)
	}

	// a supprime l'item
	a.deleteCustom("c-cable")
	sync(t, r, a)

	// b (périmé : a encore c-cable en local) resync → ne doit PAS le recréer
	m := sync(t, r, b)
	if serverHasCustom(m, "c-cable") {
		t.Errorf("BUG: device périmé a ressuscité l'item supprimé côté serveur")
	}
}

// Scénario E : un 2e device "démasque" (un-hide) un item ; l'autre device le re-cache au sync.
func (c *clientStore) hide(id string)   { c.hidden = append(c.hidden, id) }
func (c *clientStore) unhide(id string) {
	out := c.hidden[:0]
	for _, x := range c.hidden {
		if x != id {
			out = append(out, x)
		}
	}
	c.hidden = out
}
func (c *clientStore) applyHiddenUnion(resp map[string]any) {
	h, _ := resp["hidden"].([]any)
	seen := map[string]bool{}
	for _, x := range c.hidden {
		seen[x] = true
	}
	for _, x := range h {
		s := x.(string)
		if !seen[s] {
			c.hidden = append(c.hidden, s)
			seen[s] = true
		}
	}
}
func TestReproE_UnhideReverts(t *testing.T) {
	r := bootstrap(t)
	c := newClient("phone-rene")
	c.hide("doc-5")
	m := sync(t, r, c)
	c.applyHiddenUnion(m)

	c.unhide("doc-5") // l'utilisateur restaure l'item
	m = sync(t, r, c)
	c.applyHiddenUnion(m) // union → re-masque doc-5
	for _, x := range c.hidden {
		if x == "doc-5" {
			t.Errorf("BUG REPRODUIT: item restauré (un-hidden) re-masqué par l'union au sync")
		}
	}
}

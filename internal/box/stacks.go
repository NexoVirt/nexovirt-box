package box

// App stacks: several catalog apps installed together. The stack definition (apps and fixed
// answers) comes from the signed catalog index; each app is installed through the normal helper
// install (which checks the app's signature itself), one after another, skipping apps that are
// already installed. The stack job stops at the first app that fails.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

func (a *App) stackInstall(w http.ResponseWriter, r *http.Request) {
	cat, _ := a.catalog.get()
	if cat == nil {
		writeError(w, http.StatusInternalServerError, "The app catalog could not be loaded.")
		return
	}
	id := r.PathValue("id")
	var st *catalog.Stack
	for _, s := range cat.Stacks() {
		if s.ID == id {
			s := s
			st = &s
		}
	}
	if st == nil {
		writeError(w, http.StatusNotFound, "This stack is not in the catalog.")
		return
	}
	for _, sa := range st.Apps {
		if e, ok := cat.Entry(sa.ID); !ok || !archSupported(e.Architectures) {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s does not run on this server, so the stack cannot be installed.", sa.ID))
			return
		}
	}
	uid, _ := a.currentUser(r)
	name, ip := a.store.username(uid), a.clientIP(r)
	j, err := a.jobs.start("install", "stack-"+id, func(ctx context.Context, logf func(string)) (any, error) {
		installed := a.installedIDs(ctx)
		done := []string{}
		for i, sa := range st.Apps {
			if installed[sa.ID] {
				logf(fmt.Sprintf("(%d/%d) %s is already installed", i+1, len(st.Apps), sa.ID))
				continue
			}
			logf(fmt.Sprintf("(%d/%d) Installing %s", i+1, len(st.Apps), sa.ID))
			if _, err := a.helper.call(ctx, helperRequest{Action: "app_install", ID: sa.ID, Answers: sa.Answers}, logf); err != nil {
				a.store.audit(name, ip, "app.install.failed", sa.ID+" (stack "+id+")")
				return nil, userError{fmt.Sprintf("%s could not be installed: %s Apps installed before it stay.", sa.ID, friendly(err))}
			}
			a.store.audit(name, ip, "app.install", sa.ID+" (stack "+id+")")
			done = append(done, sa.ID)
		}
		a.store.audit(name, ip, "stack.install", id)
		return map[string]any{"installed": done, "notes": st.Notes}, nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, friendly(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job": j.ID})
}

func (a *App) installedIDs(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	raw, err := a.helper.call(ctx, helperRequest{Action: "apps_status"}, nil)
	if err != nil {
		return out
	}
	var list []struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &list)
	for _, x := range list {
		out[x.ID] = true
	}
	return out
}

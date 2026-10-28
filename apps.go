package main

import "net/http"

func notYet(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "Not available yet.")
}

type catalogStore struct{}

func newCatalogStore(state, url string) *catalogStore { return &catalogStore{} }

type jobs struct{}

func newJobs() *jobs { return &jobs{} }

func (a *App) dockerInfo(w http.ResponseWriter, r *http.Request)        { notYet(w, r) }
func (a *App) dockerInstall(w http.ResponseWriter, r *http.Request)     { notYet(w, r) }
func (a *App) catalogList(w http.ResponseWriter, r *http.Request)       { notYet(w, r) }
func (a *App) catalogSync(w http.ResponseWriter, r *http.Request)       { notYet(w, r) }
func (a *App) catalogIcon(w http.ResponseWriter, r *http.Request)       { notYet(w, r) }
func (a *App) catalogScreenshot(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (a *App) appsList(w http.ResponseWriter, r *http.Request)          { notYet(w, r) }
func (a *App) appInstall(w http.ResponseWriter, r *http.Request)        { notYet(w, r) }
func (a *App) appAction(w http.ResponseWriter, r *http.Request)         { notYet(w, r) }
func (a *App) appUninstall(w http.ResponseWriter, r *http.Request)      { notYet(w, r) }
func (a *App) appLogs(w http.ResponseWriter, r *http.Request)           { notYet(w, r) }
func (a *App) jobStatus(w http.ResponseWriter, r *http.Request)         { notYet(w, r) }

func (h *helperServer) dockerInfo(req helperRequest, logf func(string)) (any, error)    { return nil, uerr("not yet") }
func (h *helperServer) dockerInstall(req helperRequest, logf func(string)) (any, error) { return nil, uerr("not yet") }
func (h *helperServer) appsStatus(req helperRequest, logf func(string)) (any, error)    { return nil, uerr("not yet") }
func (h *helperServer) appInstall(req helperRequest, logf func(string)) (any, error)    { return nil, uerr("not yet") }
func (h *helperServer) appAction(req helperRequest, logf func(string)) (any, error)     { return nil, uerr("not yet") }
func (h *helperServer) appUninstall(req helperRequest, logf func(string)) (any, error)  { return nil, uerr("not yet") }
func (h *helperServer) appLogs(req helperRequest, logf func(string)) (any, error)       { return nil, uerr("not yet") }

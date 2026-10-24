//go:build runtimee2e

package runtimee2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The prebuilt runtime job has no local SPA distribution. Reproduce Vite's
// minified listener syntax without adding a frontend build prerequisite.
func TestAssertPushWorkerMinified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/push-sw.js" || r.Header.Get("Service-Worker") != "script" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.WriteString(w, "self.addEventListener(`push`,e=>{}),self.addEventListener(`notificationclick`,e=>{});")
	}))
	t.Cleanup(server.Close)
	h := &harness{t: t, baseURL: server.URL, http: server.Client()}
	h.assertPushWorker(context.Background())
}

func TestWorkerRegistersEvent(t *testing.T) {
	for _, event := range []string{"push", "notificationclick"} {
		for _, quote := range []string{`"`, "'", "`"} {
			for _, space := range []string{"", " \n"} {
				body := "self.addEventListener" + space + "(" + space + quote + event + quote + space + ", handler);"
				if !workerRegistersEvent([]byte(body), event) {
					t.Errorf("listener not recognized: %q", body)
				}
			}
		}
	}
	for _, body := range []string{
		`self.addEventListener("pushsubscriptionchange", handler);`,
		`self.addEventListener("notificationclick", handler);`,
		`self.addEventListener(push, handler);`,
		"self.addEventListener(`push${suffix}`, handler);",
		`self.addEventListener("push' , handler);`,
		`self.addEventListener("push" + suffix, handler);`,
		`self.notaddEventListener("push", handler);`,
		`const event = "push";`,
	} {
		if workerRegistersEvent([]byte(body), "push") {
			t.Errorf("unexpected push listener: %q", body)
		}
	}
}

// Command server boots the environmental DNA contamination verdict service.
//
// Persistence is controlled by DATA_DIR: when set, a file-backed event log and
// snapshot are used and the service recovers its full state on restart;
// otherwise an in-memory store is used (suitable for smoke tests).
package main

import (
	"log"
	"net/http"
	"os"

	"edna-contamination-verdict/internal/httpapi"
	"edna-contamination-verdict/internal/service"
	"edna-contamination-verdict/internal/store"
)

func main() {
	st, err := openStore()
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	engine, err := service.NewEngine(st)
	if err != nil {
		log.Fatalf("new engine: %v", err)
	}

	var opts []httpapi.Option
	if dir := os.Getenv("FRONTEND_DIR"); dir != "" {
		opts = append(opts, httpapi.WithFrontendDir(dir))
	}
	srv := httpapi.NewServer(engine, opts...)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("environmental DNA contamination verdict service listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

func openStore() (store.Store, error) {
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		return store.OpenFileStore(dir)
	}
	return store.NewMemoryStore(), nil
}

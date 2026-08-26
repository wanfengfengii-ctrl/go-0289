package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"edna-contamination-verdict/internal/service"
	"edna-contamination-verdict/internal/store"
)

func TestModel_ListBatchesWaitsForConcurrentLockBatch(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		locking  string
		want     []string
	}{
		{
			name:     "insertion_between_existing_ids",
			existing: []string{"batch-z", "batch-a"},
			locking:  "batch-m",
			want:     []string{"batch-a", "batch-m", "batch-z"},
		},
		{
			name:     "insertion_before_existing_ids",
			existing: []string{"batch-y", "batch-c"},
			locking:  "batch-b",
			want:     []string{"batch-b", "batch-c", "batch-y"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := store.NewMemoryStore()
			engine, err := service.NewEngine(st)
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}
			handler := NewServer(engine).Handler()

			if rec := doJSON(t, handler, http.MethodPost, "/api/v1/protocols", flowSpec()); rec.Code != http.StatusCreated {
				t.Fatalf("create protocol: status %d: %s", rec.Code, rec.Body.String())
			}
			for _, id := range tt.existing {
				rec := doJSON(t, handler, http.MethodPost, "/api/v1/batches/"+id+"/lock", map[string]string{"protocol_id": "p1"})
				if rec.Code != http.StatusCreated {
					t.Fatalf("lock existing batch %q: status %d: %s", id, rec.Code, rec.Body.String())
				}
			}

			commitEntered := make(chan struct{})
			releaseCommit := make(chan struct{})
			st.SetFailpoints(store.Failpoints{BeforeCommit: func() error {
				close(commitEntered)
				<-releaseCommit
				return nil
			}})

			lockBody, err := json.Marshal(map[string]string{"protocol_id": "p1"})
			if err != nil {
				t.Fatalf("marshal lock request: %v", err)
			}
			lockDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/api/v1/batches/"+tt.locking+"/lock", bytes.NewReader(lockBody))
				req.Header.Set("Content-Type", "application/json")
				handler.ServeHTTP(rec, req)
				lockDone <- rec
			}()

			<-commitEntered
			listDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/batches", nil))
				listDone <- rec
			}()

			select {
			case rec := <-listDone:
				close(releaseCommit)
				<-lockDone
				t.Fatalf("list returned status %d before the concurrent lock transaction committed: %s", rec.Code, rec.Body.String())
			case <-time.After(200 * time.Millisecond):
			}

			close(releaseCommit)
			lockRec := <-lockDone
			if lockRec.Code != http.StatusCreated {
				t.Fatalf("concurrent lock: status %d: %s", lockRec.Code, lockRec.Body.String())
			}
			listRec := <-listDone
			if listRec.Code != http.StatusOK {
				t.Fatalf("list batches: status %d: %s", listRec.Code, listRec.Body.String())
			}
			var response struct {
				Batches []string `json:"batches"`
			}
			if err := json.Unmarshal(listRec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode list response: %v", err)
			}
			if !reflect.DeepEqual(response.Batches, tt.want) {
				t.Fatalf("batches = %v, want stable order %v", response.Batches, tt.want)
			}
		})
	}
}

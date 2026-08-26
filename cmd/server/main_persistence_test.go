package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const modelServerChild = "EDNA_MODEL_SERVER_CHILD"

type modelServerProcess struct {
	cmd     *exec.Cmd
	baseURL string
	output  *bytes.Buffer
}

func startModelServer(t *testing.T, dataDir string) *modelServerProcess {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve server address: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release server address: %v", err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("find test executable: %v", err)
	}
	cmd := exec.Command(executable, "-test.run=^TestModel_DATA_DIRStoreLifetime$")
	cmd.Env = append(os.Environ(),
		modelServerChild+"=1",
		"DATA_DIR="+dataDir,
		"ADDR="+addr,
	)
	output := new(bytes.Buffer)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	process := &modelServerProcess{cmd: cmd, baseURL: "http://" + addr, output: output}
	deadline := time.Now().Add(5 * time.Second)
	client := &http.Client{Timeout: 200 * time.Millisecond}
	for time.Now().Before(deadline) {
		response, requestErr := client.Get(process.baseURL + "/api/v1/health")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return process
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	t.Fatalf("server did not become healthy: %s", output.String())
	return nil
}

func stopModelServer(t *testing.T, process *modelServerProcess) {
	t.Helper()
	if process == nil || process.cmd.ProcessState != nil {
		return
	}
	if err := process.cmd.Process.Kill(); err != nil {
		t.Fatalf("stop server: %v; output: %s", err, process.output.String())
	}
	_ = process.cmd.Wait()
}

func modelRequest(t *testing.T, process *modelServerProcess, method, path string, body any) (int, []byte) {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, process.baseURL+path, requestBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v; server output: %s", method, path, err, process.output.String())
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return response.StatusCode, payload
}

func modelProtocol(id string) map[string]any {
	well := func(row, col int) map[string]any {
		return map[string]any{"plate": "P", "row": row, "col": col}
	}
	return map[string]any{
		"id": id, "target": "target-A", "scale": 1000, "threshold": 10,
		"baseline_start": 0, "baseline_end": 4, "window": 3,
		"positive_min": 6000, "positive_max": 8000, "replicate_count": 2,
		"reagent_lot": "lot-1",
		"layout": map[string]any{
			"plate_id": "P", "rows": 8, "cols": 12,
			"samples": []any{map[string]any{
				"replicate_group": "S1",
				"tubes": []any{
					map[string]any{"tube_code": "T1", "well": well(1, 1)},
					map[string]any{"tube_code": "T2", "well": well(1, 2)},
				},
			}},
			"controls": []any{
				map[string]any{"kind": "positive", "well": well(8, 1)},
				map[string]any{"kind": "negative", "well": well(8, 2)},
			},
		},
	}
}

func TestModel_DATA_DIRStoreLifetime(t *testing.T) {
	if os.Getenv(modelServerChild) == "1" {
		main()
		return
	}

	dataDir := t.TempDir()
	server := startModelServer(t, dataDir)
	t.Cleanup(func() { stopModelServer(t, server) })

	cases := []struct {
		name       string
		method     string
		path       string
		body       any
		wantStatus int
		restart    bool
		wantText   string
	}{
		{name: "first protocol write after startup", method: http.MethodPost, path: "/api/v1/protocols", body: modelProtocol("p1"), wantStatus: http.StatusCreated},
		{name: "subsequent batch lock write", method: http.MethodPost, path: "/api/v1/batches/b1/lock", body: map[string]any{"protocol_id": "p1"}, wantStatus: http.StatusCreated},
		{name: "locked batch restored after restart", method: http.MethodGet, path: "/api/v1/batches/b1", wantStatus: http.StatusOK, restart: true, wantText: `"locked":true`},
		{name: "store remains writable after recovery", method: http.MethodPost, path: "/api/v1/protocols", body: modelProtocol("p2"), wantStatus: http.StatusCreated},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.restart {
				stopModelServer(t, server)
				server = startModelServer(t, dataDir)
			}
			status, payload := modelRequest(t, server, tc.method, tc.path, tc.body)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d; response: %s; server output: %s", status, tc.wantStatus, payload, server.output.String())
			}
			if tc.wantText != "" && !strings.Contains(string(payload), tc.wantText) {
				t.Fatalf("response %s does not contain %s", payload, tc.wantText)
			}
		})
	}
}

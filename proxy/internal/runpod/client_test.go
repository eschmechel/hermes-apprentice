package runpod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func runpodClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		apiKey:  "test-key",
		baseURL: srv.URL,
		client:  srv.Client(),
	}
}

func TestListPodsSuccess(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(podResponse{
			Data: struct {
				Myself struct {
					Pods []Pod `json:"pods"`
				} `json:"myself"`
			}{
				Myself: struct {
					Pods []Pod `json:"pods"`
				}{
					Pods: []Pod{{
						ID:            "pod-1",
						Name:          "training-pod",
						CostPerHr:     1.20,
						DesiredStatus: "RUNNING",
						Runtime: Runtime{
							UptimeInSeconds: 7200,
							GPUs:            []GPUMetrics{{GPUUtilPercent: 85, MemoryUtilPercent: 60}},
						},
					}},
				},
			},
		})
	})

	ctx := context.Background()
	sum, err := c.ListPods(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sum.Pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(sum.Pods))
	}
	pod := sum.Pods[0]
	if pod.ID != "pod-1" {
		t.Errorf("expected pod-1, got %s", pod.ID)
	}
	if pod.CostPerHr != 1.20 {
		t.Errorf("expected cost 1.20, got %f", pod.CostPerHr)
	}
	if pod.UptimeHours != 2.0 {
		t.Errorf("expected 2h uptime, got %f", pod.UptimeHours)
	}
	if pod.AccruedCost != 2.40 {
		t.Errorf("expected accrued 2.40, got %f", pod.AccruedCost)
	}
	if pod.GPUUtil != 85 {
		t.Errorf("expected gpu util 85, got %f", pod.GPUUtil)
	}
	if pod.MemoryUtil != 60 {
		t.Errorf("expected mem util 60, got %f", pod.MemoryUtil)
	}
	if sum.TotalCostHr != 1.20 {
		t.Errorf("expected total cost/hr 1.20, got %f", sum.TotalCostHr)
	}
	if sum.TotalAccrued != 2.40 {
		t.Errorf("expected total accrued 2.40, got %f", sum.TotalAccrued)
	}
}

func TestListPodsEmpty(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"myself": map[string]interface{}{
					"pods": []interface{}{},
				},
			},
		})
	})
	sum, err := c.ListPods(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sum.Pods) != 0 {
		t.Fatalf("expected no pods, got %d", len(sum.Pods))
	}
}

func TestListPodsNoGPU(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"myself": map[string]interface{}{
					"pods": []map[string]interface{}{{
						"id":            "pod-no-gpu",
						"name":          "",
						"costPerHr":     0,
						"desiredStatus": "",
						"runtime": map[string]interface{}{
							"uptimeInSeconds": 3600,
							"gpus":            []interface{}{},
						},
					}},
				},
			},
		})
	})
	sum, err := c.ListPods(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum.Pods[0].GPUUtil != 0 || sum.Pods[0].MemoryUtil != 0 {
		t.Errorf("expected zero gpu metrics, got util=%f mem=%f", sum.Pods[0].GPUUtil, sum.Pods[0].MemoryUtil)
	}
}

func TestListPodsHTTPError(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	})
	_, err := c.ListPods(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListPodsInvalidJSON(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json"))
	})
	_, err := c.ListPods(context.Background())
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestListPodsGraphQLErrors(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(podResponse{
			Errors: []struct {
				Message string `json:"message"`
			}{{Message: "permission denied"}},
		})
	})
	_, err := c.ListPods(context.Background())
	if err == nil {
		t.Fatal("expected GraphQL error")
	}
}

func TestProvisionPodSuccess(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Data struct {
				PodFindAndDeployOnDemand PodDeployResult `json:"podFindAndDeployOnDemand"`
			} `json:"data"`
		}{}
		resp.Data.PodFindAndDeployOnDemand = PodDeployResult{
			ID:            "new-pod-1",
			Name:          "burst-trainer",
			MachineID:     "m-1",
			GPUCount:      1,
			CostPerHr:     0.76,
			DesiredStatus: "RUNNING",
			ImageName:     "apprentice-vllm:latest",
		}
		json.NewEncoder(w).Encode(resp)
	})
	result, err := c.ProvisionPod(context.Background(), PodProvisionInput{
		GPUTypeID:         "NVIDIA A100 SXM4 80GB",
		GPUCount:          1,
		ContainerDiskInGb: 100,
		MinVcpuCount:      8,
		MinMemoryInGb:     32,
		Name:              "burst-trainer",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "new-pod-1" {
		t.Errorf("expected new-pod-1, got %s", result.ID)
	}
	if result.CostPerHr != 0.76 {
		t.Errorf("expected cost 0.76, got %f", result.CostPerHr)
	}
}

func TestProvisionPodEmptyID(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"podFindAndDeployOnDemand": map[string]interface{}{
					"id": "",
				},
			},
		})
	})
	_, err := c.ProvisionPod(context.Background(), PodProvisionInput{
		GPUTypeID: "NVIDIA A100",
	})
	if err == nil {
		t.Fatal("expected error for empty pod id")
	}
}

func TestProvisionPodGraphQLErrors(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []map[string]string{{"message": "not enough capacity"}},
		})
	})
	_, err := c.ProvisionPod(context.Background(), PodProvisionInput{
		GPUTypeID: "NVIDIA A100",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTerminatePodSuccess(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	err := c.TerminatePod(context.Background(), "pod-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTerminatePodError(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("pod not found"))
	})
	err := c.TerminatePod(context.Background(), "pod-1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPingSuccess(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPingError(t *testing.T) {
	c := runpodClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("invalid key"))
	})
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNew(t *testing.T) {
	c := New("my-key")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.apiKey != "my-key" {
		t.Errorf("expected my-key, got %s", c.apiKey)
	}
	if c.baseURL != graphqlURL {
		t.Errorf("expected %s, got %s", graphqlURL, c.baseURL)
	}
	if c.client == nil {
		t.Fatal("expected non-nil http client")
	}
}

func TestListPodsConnectionRefused(t *testing.T) {
	c := &Client{
		apiKey:  "test-key",
		baseURL: "http://127.0.0.1:19999",
		client:  &http.Client{},
	}
	_, err := c.ListPods(context.Background())
	if err == nil {
		t.Fatal("expected connection error")
	}
}

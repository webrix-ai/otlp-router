package store

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	saDir     = "/var/run/secrets/kubernetes.io/serviceaccount"
	configKey = "targets.yaml"
)

// ConfigMap stores targets in a Kubernetes ConfigMap through the API server,
// versioned by resourceVersion. It talks to the API directly with the pod's
// service account, which only needs get/create/update on this one ConfigMap.
type ConfigMap struct {
	name, namespace string
	base            string
	tokenPath       string
	client          *http.Client
}

func NewConfigMap(name, namespace string) (*ConfigMap, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return nil, fmt.Errorf("configmap store needs to run inside Kubernetes (KUBERNETES_SERVICE_HOST is unset)")
	}
	if namespace == "" {
		ns, err := os.ReadFile(saDir + "/namespace")
		if err != nil {
			return nil, fmt.Errorf("read namespace: %w", err)
		}
		namespace = strings.TrimSpace(string(ns))
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	return &ConfigMap{
		name:      name,
		namespace: namespace,
		base:      "https://" + host + ":" + port,
		tokenPath: saDir + "/token",
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		},
	}, nil
}

func (c *ConfigMap) Kind() string { return "configmap" }

type cmObject struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   cmMeta            `json:"metadata"`
	Data       map[string]string `json:"data"`
}

type cmMeta struct {
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

func (c *ConfigMap) url(withName bool) string {
	u := fmt.Sprintf("%s/api/v1/namespaces/%s/configmaps", c.base, c.namespace)
	if withName {
		u += "/" + c.name
	}
	return u
}

func (c *ConfigMap) do(ctx context.Context, method, url string, body any) (*http.Response, []byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, nil, err
	}
	// The projected token rotates, so it is re-read on every request.
	token, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	out, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return res, out, err
}

func (c *ConfigMap) Load(ctx context.Context) (State, string, bool, error) {
	res, body, err := c.do(ctx, http.MethodGet, c.url(true), nil)
	if err != nil {
		return State{}, "", false, err
	}
	if res.StatusCode == http.StatusNotFound {
		return State{}, "", false, nil
	}
	if res.StatusCode != http.StatusOK {
		return State{}, "", false, fmt.Errorf("get configmap: HTTP %d: %s", res.StatusCode, body)
	}
	var cm cmObject
	if err := json.Unmarshal(body, &cm); err != nil {
		return State{}, "", false, err
	}
	raw, ok := cm.Data[configKey]
	if !ok {
		return State{}, cm.Metadata.ResourceVersion, false, nil
	}
	state, err := decode([]byte(raw))
	return state, cm.Metadata.ResourceVersion, true, err
}

func (c *ConfigMap) Save(ctx context.Context, state State, version string) (string, error) {
	raw, err := encode(state)
	if err != nil {
		return "", err
	}
	method, url := http.MethodPost, c.url(false)
	cm := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": c.name, "namespace": c.namespace},
	}
	if version != "" {
		// Update only data.targets.yaml and keep everything else, including
		// the labels and annotations Helm owns on this object.
		res, body, err := c.do(ctx, http.MethodGet, c.url(true), nil)
		if err != nil {
			return "", err
		}
		if res.StatusCode != http.StatusOK {
			return "", fmt.Errorf("get configmap: HTTP %d: %s", res.StatusCode, body)
		}
		cm = map[string]any{}
		if err := json.Unmarshal(body, &cm); err != nil {
			return "", err
		}
		meta, _ := cm["metadata"].(map[string]any)
		if meta == nil || meta["resourceVersion"] != version {
			return "", ErrConflict
		}
		method, url = http.MethodPut, c.url(true)
	}
	data, _ := cm["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	data[configKey] = string(raw)
	cm["data"] = data
	res, body, err := c.do(ctx, method, url+"?fieldManager=otlp-router", cm)
	if err != nil {
		return "", err
	}
	if res.StatusCode == http.StatusConflict {
		return "", ErrConflict
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("save configmap: HTTP %d: %s", res.StatusCode, body)
	}
	var saved cmObject
	if err := json.Unmarshal(body, &saved); err != nil {
		return "", err
	}
	return saved.Metadata.ResourceVersion, nil
}

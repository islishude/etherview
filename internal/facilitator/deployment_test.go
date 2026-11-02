package facilitator

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderedHelmConfig(t *testing.T) {
	path := os.Getenv("HELM_RENDERED_CONFIG")
	if path == "" {
		t.Skip("run deploy/facilitator/helm/test.sh for rendered chart acceptance")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(b)))
	found := false
	envFound := false
	expectEnv := os.Getenv("HELM_EXPECT_SECRET_ENV") == "true"
	for {
		var doc struct {
			Kind string            `yaml:"kind"`
			Data map[string]string `yaml:"data"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Name string `yaml:"name"`
							Env  []struct {
								Name      string  `yaml:"name"`
								Value     *string `yaml:"value"`
								ValueFrom struct {
									SecretKeyRef struct {
										Name string `yaml:"name"`
										Key  string `yaml:"key"`
									} `yaml:"secretKeyRef"`
								} `yaml:"valueFrom"`
							} `yaml:"env"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		err = decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if doc.Kind == "Deployment" {
			for _, container := range doc.Spec.Template.Spec.Containers {
				if !expectEnv {
					if len(container.Env) != 0 {
						t.Fatal("unexpected credential env")
					}
					continue
				}
				if container.Name != "facilitator" || len(container.Env) != 2 {
					t.Fatal("wrong credential recipient")
				}
				seen := map[string]bool{}
				for _, env := range container.Env {
					if env.Name != "X402_DATABASE_URL" && env.Name != "X402_BEARER_TOKEN" {
						t.Fatal("unexpected env name")
					}
					if seen[env.Name] || env.Value != nil || env.ValueFrom.SecretKeyRef.Name != "env-secrets" || env.ValueFrom.SecretKeyRef.Key != env.Name {
						t.Fatal("credential must come from expected Secret key")
					}
					seen[env.Name] = true
				}
				envFound = true
			}
		}
		if doc.Kind != "ConfigMap" {
			continue
		}
		body, ok := doc.Data["facilitator.yaml"]
		if !ok {
			continue
		}
		configPath := filepath.Join(t.TempDir(), "facilitator.yaml")
		if err = os.WriteFile(configPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		config, err := LoadConfig(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if expectEnv && (config.Database.URLFile != "" || config.Auth.BearerTokenFile != "") {
			t.Fatal("expected environment-only credential config")
		}
		found = true
	}
	if expectEnv && !envFound {
		t.Fatal("rendered credential env missing")
	}
	if !found {
		t.Fatal("rendered facilitator config missing")
	}
}

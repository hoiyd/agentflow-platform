package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
)

// Exercise the production startup path with a real controlled CLI process.
// This proves readiness/recovery/lock ownership, not actual microVM isolation.
func TestSandboxStartupReadinessAndRecovery(t *testing.T) {
	for _, scenario := range []string{"disabled", "missing-cli", "invalid-profile", "unsupported-cli", "unauthenticated", "orphan-recovered", "orphan-unconfirmed"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "state")
			cli := filepath.Join(root, "sbx-fixture")
			name := "agentflow-0123456789abcdef0123456789abcdef"
			script := "#!/bin/sh\ncase \"$1\" in\ncreate) printf '%s' '--skills --deny-network --cpus --memory --template';;\nls) exit 0;;\nrm) exit 0;;\nesac\n"
			if scenario == "unsupported-cli" {
				script = "#!/bin/sh\nexit 0\n"
			} else if scenario == "unauthenticated" {
				script = strings.ReplaceAll(script, "ls) exit 0", "ls) exit 2")
			} else if scenario == "orphan-unconfirmed" {
				script = strings.ReplaceAll(script, "rm) exit 0", "rm) exit 2")
				script = strings.ReplaceAll(script, "ls) exit 0", "ls) printf '%s' '"+name+"'")
			}
			if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{SandboxEnabled: scenario != "disabled", SandboxExecutable: cli, SandboxStateDirectory: state, SandboxAllowedCommands: "/bin/sh"}
			if scenario == "missing-cli" {
				cfg.SandboxExecutable = filepath.Join(root, "missing")
			}
			if scenario == "invalid-profile" {
				cfg.SandboxCPUs = -1
			}
			if strings.HasPrefix(scenario, "orphan") {
				if err := os.Mkdir(state, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(state, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner, err := newSandboxRunner(cfg)
			switch scenario {
			case "disabled":
				if err != nil || runner != nil {
					t.Fatalf("disabled: %v %v", runner, err)
				}
			case "orphan-recovered":
				if err != nil || runner == nil {
					t.Fatal("startup", err)
				}
				if _, err := os.Stat(filepath.Join(state, name)); !os.IsNotExist(err) {
					t.Fatal("orphan record retained", err)
				}
				if err := runner.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			default:
				if err == nil || runner != nil {
					t.Fatalf("unsafe startup accepted: %v %v", runner, err)
				}
			}
			// Failed readiness must release the directory lock; repair and retry
			// must not require restarting the API process or deleting lock files.
			if scenario == "unsupported-cli" || scenario == "unauthenticated" || scenario == "orphan-unconfirmed" {
				if err := os.WriteFile(cli, []byte("#!/bin/sh\ncase \"$1\" in create) printf '%s' '--skills --deny-network --cpus --memory --template';; esac\n"), 0700); err != nil {
					t.Fatal(err)
				}
				runner, err = newSandboxRunner(cfg)
				if err != nil {
					t.Fatal("restart after repair", err)
				}
				if err := runner.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestSandboxProductionCompositionReleasesFailedStartup(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("SANDBOX_MODEL_FIXTURE_KEY", "fixture-only")
	t.Setenv("TAVILY_API_KEY", "")
	cli, routePath, toolsPath := filepath.Join(root, "sbx-fixture"), filepath.Join(root, "routes.json"), filepath.Join(root, "tools.json")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\ncase \"$1\" in create) printf '%s' '--skills --deny-network --cpus --memory --template';; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(routePath, []byte(`{"routes":[{"id":"fixture","base_url":"https://fixture.invalid/v1","model":"fixture","credential_environment":"SANDBOX_MODEL_FIXTURE_KEY","request_timeout_seconds":1,"capabilities":{"tool_calling":true,"structured_output":true,"streaming":true},"context_window_tokens":128000,"max_output_tokens":8192,"priority":100,"pricing":{"source":"fixture"}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolsPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DatabaseURL: url, AuthMode: "local", AllowedOrigins: "http://localhost:3000", ModelRouteConfigPath: routePath, ToolConfigPath: toolsPath, SandboxEnabled: true, SandboxExecutable: cli, SandboxStateDirectory: filepath.Join(root, "state"), SandboxAllowedCommands: "/bin/sh"}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "create tools manager") {
		t.Fatal("wrong startup failure", err)
	}
	// The failed dependency build must close its Runner rather than retain its
	// ownership lock. Validate through a second real production construction.
	if err := tool.SaveConfig(toolsPath, tool.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	application, err := New(cfg)
	if err != nil {
		t.Fatal("repaired production startup", err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	response := httptest.NewRecorder()
	application.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/tools", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"name":"sandbox_command"`) {
		t.Fatalf("Tool not published: %d %s", response.Code, response.Body.String())
	}
	if err := application.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg.SandboxExecutable = filepath.Join(root, "missing-cli")
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "initialize sandbox runner") {
		t.Fatal("missing CLI accepted by production startup", err)
	}
}

func TestApplicationShutdownCancelsSandboxBeforeDrainingRun(t *testing.T) {
	root := t.TempDir()
	cli, started, cleaned := filepath.Join(root, "sbx-fixture"), filepath.Join(root, "started"), filepath.Join(root, "cleaned")
	script := "#!/bin/sh\ncase \"$1\" in create) printf '%s' '--skills --deny-network --cpus --memory --template';; exec) printf x > '" + started + "'; exec /bin/sleep 60;; rm) printf x > '" + cleaned + "';; esac\n"
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner, err := newSandboxRunner(config.Config{SandboxEnabled: true, SandboxExecutable: cli, SandboxStateDirectory: filepath.Join(root, "state"), SandboxAllowedCommands: "/bin/sh"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close(context.Background()) })
	controller := concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1})
	reservation, err := controller.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	release, err := reservation.Start(ctx, "sandbox-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, []string{"/bin/sh"})
		release()
		finished <- err
	}()
	defer func() { cancel(); <-finished }()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture did not start")
		case <-time.After(time.Millisecond):
		}
	}
	application := &Application{sandboxRunner: runner, runController: controller}
	if err := application.Close(ctx); err != nil {
		t.Fatal("graceful close did not drain canceled sandbox Run", err)
	}
	if _, err := os.Stat(cleaned); err != nil {
		t.Fatal("VM cleanup not acknowledged", err)
	}
	if err := application.Close(ctx); err != nil {
		t.Fatal("repeated close", err)
	}
	if _, err := runner.Run(ctx, []string{"/bin/sh"}); errors.Is(err, context.Canceled) || err == nil {
		t.Fatal("closed runner did not fail closed", err)
	}
}

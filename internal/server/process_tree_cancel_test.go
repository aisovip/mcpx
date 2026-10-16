package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"mcpx/internal/terminal"
	"mcpx/internal/winproc"
)

func TestReviewProcessTreeFixtureChild(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--review-process-tree" {
			continue
		}
		role, ready, late := os.Args[i+1], os.Args[i+2], os.Args[i+3]
		if role == "grandchild" {
			if err := os.WriteFile(ready, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				os.Exit(2)
			}
			time.Sleep(1500 * time.Millisecond)
			if err := os.WriteFile(late, []byte("late-grandchild"), 0600); err != nil {
				os.Exit(3)
			}
			os.Exit(0)
		}
		executable, err := os.Executable()
		if err != nil {
			os.Exit(4)
		}
		child := exec.Command(executable, "-test.run=^TestReviewProcessTreeFixtureChild$", "--", "--review-process-tree", "grandchild", ready, late)
		winproc.ConfigureNoWindow(child)
		if role == "inherited" {
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
		}
		if role == "parent-exits" {
			if err := child.Start(); err != nil {
				os.Exit(6)
			}
			os.Exit(0)
		}
		if err := child.Run(); err != nil {
			os.Exit(5)
		}
		os.Exit(0)
	}
}

func TestReviewCancelStopsGrandchildEffects(t *testing.T) {
	for _, mode := range []string{"redirected", "inherited", "parent-exits"} {
		t.Run(mode, func(t *testing.T) {
			rt := newWorkspaceRuntime(t, "tree")
			s := operationTestSession(t, rt, "tree")
			rt.cfg.Security.Commands.Default = "allow"
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ready := filepath.Join(s.WorkspacePath, "ready")
			late := filepath.Join(s.WorkspacePath, "late")
			started := callOperationTool(t, rt, "execute", map[string]any{"remote_session_id": s.ID, "purpose": "进程树取消故障复现", "action": "run", "yield_time_ms": 1, "argv": []any{executable, "-test.run=^TestReviewProcessTreeFixtureChild$", "--", "--review-process-tree", mode, ready, late}, "shell": false})
			if started["status"] != "accepted" {
				t.Fatalf("Task 未启动: %+v", started)
			}
			data, _ := started["data"].(map[string]any)
			taskID, _ := data["execution_task_id"].(string)
			if taskID == "" {
				t.Fatalf("execution task id missing: %+v", started)
			}
			deadline := time.Now().Add(time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("孙进程未启动")
				}
				time.Sleep(5 * time.Millisecond)
			}
			callOperationTool(t, rt, "execute", map[string]any{"remote_session_id": s.ID, "purpose": "停止进程树回归", "action": "stop", "execution_task_id": taskID})
			task, err := rt.tasks.Get(s.ID, taskID)
			if err != nil {
				t.Fatal(err)
			}
			waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = task.Wait(waitCtx)
			if task.StatusView()["status"] != terminal.TaskKilled {
				t.Fatalf("Task stop did not reach killed state: %+v", task.StatusView())
			}
			time.Sleep(100 * time.Millisecond)
			pidBytes, pidErr := os.ReadFile(ready)
			pid, parseErr := strconv.Atoi(string(pidBytes))
			if pidErr != nil || parseErr != nil {
				t.Fatal("缺少孙进程身份")
			}
			alive, aliveErr := fixtureProcessAlive(pid)
			if aliveErr != nil || alive {
				t.Fatalf("停止后孙进程仍存活或无法核对: alive=%v err=%v", alive, aliveErr)
			}
			// Give the fixture time to attempt its delayed write after cancellation.
			time.Sleep(1600 * time.Millisecond)
			if _, err := os.Stat(late); !os.IsNotExist(err) {
				t.Fatal("停止 Task 后孙进程仍产生文件效果")
			}
		})
	}
}

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
)

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>一键root</title>
  <style>
		* {
			box-sizing: border-box;
		}

    body {
      max-width: 900px;
			margin: 0 auto;
			padding: 20px 14px;
      font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #f6f8fa;
      color: #24292f;
    }

		h1 {
			margin: 0 0 16px;
			font-size: 28px;
			line-height: 1.2;
		}

		.actions {
			display: grid;
			grid-template-columns: 1fr 1fr;
			gap: 10px;
		}

    button {
			width: 100%;
      padding: 10px 18px;
      border: 0;
      border-radius: 6px;
      background: #1f883d;
      color: white;
      cursor: pointer;
      font-size: 16px;
    }
		button.secondary {
			background: #57606a;
		}
    button:disabled {
      background: #8c959f;
      cursor: not-allowed;
    }
    pre {
			min-height: 55vh;
      margin-top: 18px;
      padding: 14px;
      overflow: auto;
      border-radius: 6px;
      background: #0d1117;
      color: #e6edf3;
      white-space: pre-wrap;
      word-break: break-word;
    }

		@media (min-width: 640px) {
			body {
				padding: 40px 20px;
			}

			.actions {
				display: flex;
			}

			button {
				width: auto;
			}

			pre {
				min-height: 420px;
			}
		}
  </style>
</head>
<body>
  <h1>一键root</h1>
	<div class="actions">
		<button id="run" disabled>ROOT</button>
		<button id="clear" class="secondary" type="button">Clear</button>
	</div>
  <pre id="log"></pre>

  <script>
    const button = document.getElementById("run");
		const clearButton = document.getElementById("clear");
    const log = document.getElementById("log");
		let streaming = false;

		clearButton.addEventListener("click", () => {
			log.textContent = "";
		});

		async function streamLogs(url, options = {}) {
			if (streaming) {
				return;
			}

			streaming = true;
			button.disabled = true;
			log.textContent = "";

			try {
				const response = await fetch(url, options);
				const reader = response.body.getReader();
				const decoder = new TextDecoder();

				while (true) {
					const result = await reader.read();
					if (result.done) {
						break;
					}

					log.textContent += decoder.decode(result.value, { stream: true });
					log.scrollTop = log.scrollHeight;
				}

				log.textContent += decoder.decode();
				if (!response.ok) {
					log.textContent += "\n请求失败，HTTP 状态码: " + response.status + "\n";
				}
			} catch (error) {
				log.textContent += "\n请求失败: " + error + "\n";
			} finally {
				streaming = false;
				button.disabled = false;
			}
		}

		async function loadStatus() {
			try {
				const response = await fetch("/status");
				const status = await response.json();
				if (status.running) {
					streamLogs("/stream");
					return;
				}

				button.disabled = false;
				log.textContent = status.log || "";
			} catch (error) {
				log.textContent += "\n状态获取失败: " + error + "\n";
				button.disabled = false;
			}
		}

    button.addEventListener("click", async () => {
			streamLogs("/run", { method: "POST" });
    });

		loadStatus();
  </script>
</body>
</html>`))

type scriptRunner struct {
	mu      sync.Mutex
	running bool
	log     []byte
	done    chan struct{}
	update  chan struct{}
	script  string
}

func newScriptRunner(script string) *scriptRunner {
	return &scriptRunner{
		script: script,
		update: make(chan struct{}),
	}
}

func (sr *scriptRunner) start() bool {
	sr.mu.Lock()
	if sr.running {
		sr.mu.Unlock()
		return false
	}

	sr.running = true
	sr.log = nil
	sr.done = make(chan struct{})
	sr.update = make(chan struct{})
	sr.mu.Unlock()

	go func() {
		writer := runnerWriter{runner: sr}
		if err := runScript(writer, sr.script); err != nil {
			fmt.Fprintf(writer, "\n脚本执行失败: %v\n", err)
			log.Printf("script failed: %v", err)
		}
		sr.finish()
	}()

	return true
}

func (sr *scriptRunner) append(p []byte) {
	data := append([]byte(nil), p...)

	sr.mu.Lock()
	sr.log = append(sr.log, data...)
	close(sr.update)
	sr.update = make(chan struct{})
	sr.mu.Unlock()
}

func (sr *scriptRunner) finish() {
	sr.mu.Lock()
	if sr.running {
		sr.running = false
		close(sr.done)
		close(sr.update)
		sr.update = make(chan struct{})
	}
	sr.mu.Unlock()
}

func (sr *scriptRunner) snapshot() ([]byte, bool, <-chan struct{}, <-chan struct{}) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	data := append([]byte(nil), sr.log...)
	return data, sr.running, sr.update, sr.done
}

type runnerWriter struct {
	runner *scriptRunner
}

func (rw runnerWriter) Write(p []byte) (int, error) {
	rw.runner.append(p)
	return len(p), nil
}

type statusResponse struct {
	Running bool   `json:"running"`
	Log     string `json:"log"`
}

func streamRunner(w http.ResponseWriter, r *http.Request, runner *scriptRunner) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	offset := 0
	for {
		data, running, update, done := runner.snapshot()
		if offset < len(data) {
			if _, err := w.Write(data[offset:]); err != nil {
				return
			}
			offset = len(data)
			flusher.Flush()
		}

		if !running {
			return
		}

		select {
		case <-update:
		case <-done:
		case <-r.Context().Done():
			return
		}
	}
}

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	script := flag.String("script", "", "shell script path")
	flag.Parse()

	if *script == "" {
		log.Fatal("missing required -script argument")
	}
	if err := validateScript(*script); err != nil {
		log.Fatalf("invalid script path: %v", err)
	}

	runner := newScriptRunner(*script)
	mux := http.NewServeMux()
	mux.HandleFunc("/", handlePage)
	mux.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		handleRun(w, r, runner)
	})
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		handleStream(w, r, runner)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		handleStatus(w, r, runner)
	})

	log.Printf("listening on %s, script: %s", *addr, *script)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func validateScript(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("path is a directory")
	}
	return nil
}

func handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := page.Execute(w, nil); err != nil {
		log.Printf("render page: %v", err)
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request, runner *scriptRunner) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	data, running, _, _ := runner.snapshot()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(statusResponse{Running: running, Log: string(data)}); err != nil {
		log.Printf("encode status: %v", err)
	}
}

func handleStream(w http.ResponseWriter, r *http.Request, runner *scriptRunner) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	streamRunner(w, r, runner)
}

func handleRun(w http.ResponseWriter, r *http.Request, runner *scriptRunner) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runner.start()
	streamRunner(w, r, runner)
}

func runScript(output io.Writer, script string) error {
	cmd := exec.Command("/bin/sh", script)
	cmd.Stdout = output
	cmd.Stderr = output

	fmt.Fprintf(output, "开始执行: %s\n\n", script)
	if err := cmd.Start(); err != nil {
		return err
	}

	err := cmd.Wait()
	if err != nil {
		return err
	}

	fmt.Fprint(output, "\n脚本执行完成\n")
	return nil
}

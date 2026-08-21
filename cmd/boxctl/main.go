package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/net/websocket"
)

type startProcessRequest struct {
	Cmd  []string `json:"cmd"`
	Cwd  string   `json:"cwd,omitempty"`
	TTY  bool     `json:"tty,omitempty"`
	Rows uint16   `json:"rows,omitempty"`
	Cols uint16   `json:"cols,omitempty"`
}

type startProcessResponse struct {
	Process processInfo `json:"process"`
}

type processInfo struct {
	ID  string `json:"id"`
	PID int    `json:"pid,omitempty"`
}

type apiClient struct {
	base string
}

type templateCreateResponse struct {
	TemplateID string `json:"templateID"`
	BuildID    string `json:"buildID"`
}

func main() {
	var apiAddr string
	var proxyAddr string

	root := &cobra.Command{
		Use:   "boxctl",
		Short: "NovitaBox command line client",
	}
	root.PersistentFlags().StringVar(&apiAddr, "api", "http://127.0.0.1:8080", "boxapi base URL")
	root.PersistentFlags().StringVar(&proxyAddr, "proxy", "http://127.0.0.1:8082", "boxproxy base URL")

	root.AddCommand(newSandboxCommand(&apiAddr, &proxyAddr))
	root.AddCommand(newTemplateCommand(&apiAddr))
	root.AddCommand(newImageCommand(&apiAddr))
	root.AddCommand(newRuntimeCommand(&apiAddr))
	root.AddCommand(newExecCommand(&proxyAddr, "exec [-it] <sandbox_id> <cmd> [args...]", "Execute a command in a sandbox"))

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "boxctl: %v\n", err)
		os.Exit(1)
	}
}

func newSandboxCommand(apiAddr *string, proxyAddr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sandbox",
		Aliases: []string{"sbx"},
		Short:   "Manage sandboxes",
	}

	var createTemplateID string
	var createImageID string
	var createSnapshotID string
	var createRuntimeType string
	var createGPUCount uint32
	var createOverlayBDImage string
	createCmd := &cobra.Command{
		Use:   "create [template_id]",
		Short: "Create a sandbox",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			templateID := createTemplateID
			if templateID == "" && len(args) > 0 {
				templateID = args[0]
			}
			if templateID == "" && createImageID == "" && createSnapshotID == "" && createOverlayBDImage == "" {
				return fmt.Errorf("template id, image id, snapshot id, or overlaybd image is required")
			}
			body := map[string]any{
				"templateID": templateID,
			}
			if createImageID != "" {
				body["image_id"] = createImageID
			}
			if createSnapshotID != "" {
				body["snapshot_id"] = createSnapshotID
			}
			if createGPUCount > 0 {
				body["gpu"] = createGPUCount
			}
			if createRuntimeType != "" {
				body["runtime_type"] = createRuntimeType
			}
			if createOverlayBDImage != "" {
				body["runtime_type"] = "gvisor"
				body["rootfs"] = map[string]any{
					"provider": "overlaybd",
					"image":    createOverlayBDImage,
					"pullMode": "lazy",
				}
			}
			return requestAndPrint(*apiAddr, http.MethodPost, "/v1/sandboxes", body)
		},
	}
	createCmd.Flags().StringVar(&createTemplateID, "template", "", "template id")
	createCmd.Flags().StringVar(&createRuntimeType, "runtime", "", "runtime type")
	createCmd.Flags().StringVar(&createImageID, "image", "", "image id")
	createCmd.Flags().StringVar(&createSnapshotID, "snapshot", "", "snapshot id")
	createCmd.Flags().Uint32Var(&createGPUCount, "gpu", 0, "GPU count")
	createCmd.Flags().StringVar(&createOverlayBDImage, "overlaybd-image", "", "create a gVisor sandbox directly from an OverlayBD image")

	cmd.AddCommand(createCmd)
	cmd.AddCommand(listAPICommand(apiAddr, "list", "List sandboxes", "/v1/sandboxes"))
	cmd.AddCommand(simpleAPICommand(apiAddr, "get <sandbox_id>", "Get sandbox information", http.MethodGet, "/v1/sandboxes/%s", 1, nil))
	cmd.AddCommand(simpleAPICommand(apiAddr, "pause <sandbox_id>", "Pause a sandbox", http.MethodPost, "/v1/sandboxes/%s/pause", 1, map[string]any{}))
	cmd.AddCommand(simpleAPICommand(apiAddr, "resume <sandbox_id>", "Resume a sandbox", http.MethodPost, "/v1/sandboxes/%s/resume", 1, map[string]any{}))
	cmd.AddCommand(simpleAPICommand(apiAddr, "poweroff <sandbox_id>", "Power off a sandbox", http.MethodPost, "/v1/sandboxes/%s/poweroff", 1, map[string]any{}))
	cmd.AddCommand(simpleAPICommand(apiAddr, "poweron <sandbox_id>", "Power on a sandbox", http.MethodPost, "/v1/sandboxes/%s/poweron", 1, map[string]any{}))
	cmd.AddCommand(simpleAPICommand(apiAddr, "reboot <sandbox_id>", "Reboot a sandbox", http.MethodPost, "/v1/sandboxes/%s/reboot", 1, map[string]any{}))

	deleteCmd := simpleAPICommand(apiAddr, "delete <sandbox_id>", "Delete a sandbox", http.MethodDelete, "/v1/sandboxes/%s", 1, nil)
	deleteCmd.Aliases = []string{"kill", "rm"}
	cmd.AddCommand(deleteCmd)

	stopCmd := simpleAPICommand(apiAddr, "stop <sandbox_id>", "Power off a sandbox", http.MethodPost, "/v1/sandboxes/%s/poweroff", 1, map[string]any{})
	stopCmd.Hidden = true
	startCmd := simpleAPICommand(apiAddr, "start <sandbox_id>", "Power on a sandbox", http.MethodPost, "/v1/sandboxes/%s/poweron", 1, map[string]any{})
	startCmd.Hidden = true
	restartCmd := simpleAPICommand(apiAddr, "restart <sandbox_id>", "Reboot a sandbox", http.MethodPost, "/v1/sandboxes/%s/reboot", 1, map[string]any{})
	restartCmd.Hidden = true
	cmd.AddCommand(stopCmd, startCmd, restartCmd)

	cmd.AddCommand(newSandboxExecCommand(proxyAddr))
	cmd.AddCommand(newShellCommand(proxyAddr))
	cmd.AddCommand(newSandboxBalloonCommand(apiAddr))

	return cmd
}

func newSandboxBalloonCommand(apiAddr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "balloon",
		Short: "Manage Firecracker balloon memory",
	}

	var amountMiB uint32
	setCmd := &cobra.Command{
		Use:   "set <sandbox_id>",
		Short: "Set the Firecracker balloon target in MiB",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return requestAndPrint(*apiAddr, http.MethodPatch, fmt.Sprintf("/v1/sandboxes/%s/balloon", url.PathEscape(args[0])), map[string]any{"amountMiB": amountMiB})
		},
	}
	setCmd.Flags().Uint32Var(&amountMiB, "amount-mib", 0, "balloon target in MiB")

	var interval uint32
	statsCmd := &cobra.Command{
		Use:   "stats <sandbox_id>",
		Short: "Get Firecracker balloon statistics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return requestAndPrint(*apiAddr, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%s/balloon/statistics", url.PathEscape(args[0])), nil)
		},
	}

	statsIntervalCmd := &cobra.Command{
		Use:   "stats-interval <sandbox_id>",
		Short: "Set Firecracker balloon statistics polling interval",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return requestAndPrint(*apiAddr, http.MethodPatch, fmt.Sprintf("/v1/sandboxes/%s/balloon/statistics", url.PathEscape(args[0])), map[string]any{"statsPollingIntervalS": interval})
		},
	}
	statsIntervalCmd.Flags().Uint32Var(&interval, "interval-s", 1, "statistics polling interval in seconds")

	hintingCmd := &cobra.Command{
		Use:   "hinting",
		Short: "Manage Firecracker free-page hinting",
	}
	var acknowledgeOnStop bool
	hintingCmd.AddCommand(&cobra.Command{
		Use:   "get <sandbox_id>",
		Short: "Get free-page hinting status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return requestAndPrint(*apiAddr, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%s/balloon/hinting", url.PathEscape(args[0])), nil)
		},
	})
	startHintingCmd := &cobra.Command{
		Use:   "start <sandbox_id>",
		Short: "Start free-page hinting",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return requestAndPrint(*apiAddr, http.MethodPost, fmt.Sprintf("/v1/sandboxes/%s/balloon/hinting/start", url.PathEscape(args[0])), map[string]any{"acknowledgeOnStop": acknowledgeOnStop})
		},
	}
	startHintingCmd.Flags().BoolVar(&acknowledgeOnStop, "acknowledge-on-stop", true, "acknowledge completion after stopping the hinting run")
	hintingCmd.AddCommand(startHintingCmd)
	hintingCmd.AddCommand(simpleAPICommand(apiAddr, "stop <sandbox_id>", "Stop free-page hinting", http.MethodPost, "/v1/sandboxes/%s/balloon/hinting/stop", 1, map[string]any{}))

	cmd.AddCommand(setCmd)
	cmd.AddCommand(simpleAPICommand(apiAddr, "get <sandbox_id>", "Get Firecracker balloon configuration", http.MethodGet, "/v1/sandboxes/%s/balloon", 1, nil))
	cmd.AddCommand(statsCmd, statsIntervalCmd, hintingCmd)
	return cmd
}

func newTemplateCommand(apiAddr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "template",
		Aliases: []string{"tpl"},
		Short:   "Manage templates",
	}

	cmd.AddCommand(newTemplateCreateCommand(apiAddr))
	cmd.AddCommand(newTemplateBuildCommand(apiAddr))
	cmd.AddCommand(listAPICommand(apiAddr, "list", "List templates", "/templates"))
	cmd.AddCommand(simpleAPICommand(apiAddr, "get <template_id>", "Get template information", http.MethodGet, "/templates/%s", 1, nil))
	cmd.AddCommand(simpleAPICommand(apiAddr, "status <template_id> <build_id>", "Get template build status", http.MethodGet, "/v2/templates/%s/builds/%s/status", 2, nil))
	cmd.AddCommand(newTemplateConvertCommand(apiAddr))

	deleteCmd := simpleAPICommand(apiAddr, "delete <template_id>", "Delete a template", http.MethodDelete, "/templates/%s", 1, nil)
	deleteCmd.Aliases = []string{"rm"}
	cmd.AddCommand(deleteCmd)

	return cmd
}

func newTemplateCreateCommand(apiAddr *string) *cobra.Command {
	var templateID string
	var cpuCount int32
	var memoryMB int32
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a template record and build id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{
				"name": args[0],
			}
			if templateID != "" {
				body["templateID"] = templateID
			}
			if cpuCount > 0 {
				body["cpuCount"] = cpuCount
			}
			if memoryMB > 0 {
				body["memoryMB"] = memoryMB
			}
			return requestAndPrint(*apiAddr, http.MethodPost, "/v3/templates", body)
		},
	}
	cmd.Flags().StringVar(&templateID, "template", "", "template id")
	cmd.Flags().Int32Var(&cpuCount, "cpu", 0, "template CPU count")
	cmd.Flags().Int32Var(&memoryMB, "memory", 0, "template memory in MB")
	return cmd
}

func newTemplateBuildCommand(apiAddr *string) *cobra.Command {
	var templateID string
	var cpuCount int32
	var memoryMB int32
	var fromImage string
	var fromTemplate string
	var startCmd string
	var readyCmd string
	var runSteps []string
	var execSteps []string

	cmd := &cobra.Command{
		Use:   "build <name>",
		Short: "Create and build a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := apiClient{base: *apiAddr}
			createBody := map[string]any{
				"name": args[0],
			}
			if templateID != "" {
				createBody["templateID"] = templateID
			}
			if cpuCount > 0 {
				createBody["cpuCount"] = cpuCount
			}
			if memoryMB > 0 {
				createBody["memoryMB"] = memoryMB
			}

			createData, err := client.request(http.MethodPost, "/v3/templates", createBody)
			if err != nil {
				return err
			}
			var createResp templateCreateResponse
			if err := json.Unmarshal(createData, &createResp); err != nil {
				return fmt.Errorf("decode template create response: %w", err)
			}
			if createResp.TemplateID == "" || createResp.BuildID == "" {
				return fmt.Errorf("template create response missing templateID or buildID")
			}

			buildBody := map[string]any{}
			if fromImage != "" {
				buildBody["fromImage"] = fromImage
			}
			if fromTemplate != "" {
				buildBody["fromTemplate"] = fromTemplate
			}
			if startCmd != "" {
				buildBody["startCmd"] = startCmd
			}
			if readyCmd != "" {
				buildBody["readyCmd"] = readyCmd
			}
			steps := make([]map[string]any, 0, len(runSteps)+len(execSteps))
			for _, step := range runSteps {
				steps = append(steps, map[string]any{
					"type": "RUN",
					"args": []string{step},
				})
			}
			for _, step := range execSteps {
				args := strings.Fields(step)
				if len(args) == 0 {
					return fmt.Errorf("--exec command cannot be empty")
				}
				steps = append(steps, map[string]any{
					"type": "EXEC",
					"args": args,
				})
			}
			if len(steps) > 0 {
				buildBody["steps"] = steps
			}

			path := fmt.Sprintf("/v2/templates/%s/builds/%s", url.PathEscape(createResp.TemplateID), url.PathEscape(createResp.BuildID))
			if _, err := client.request(http.MethodPost, path, buildBody); err != nil {
				return err
			}
			return printJSON(map[string]string{
				"templateID": createResp.TemplateID,
				"buildID":    createResp.BuildID,
				"status":     "accepted",
			})
		},
	}
	cmd.Flags().StringVar(&templateID, "template", "", "template id")
	cmd.Flags().Int32Var(&cpuCount, "cpu", 0, "template CPU count")
	cmd.Flags().Int32Var(&memoryMB, "memory", 0, "template memory in MB")
	cmd.Flags().StringVar(&fromImage, "from-image", "", "source docker image")
	cmd.Flags().StringVar(&fromTemplate, "from-template", "", "source template id")
	cmd.Flags().StringVar(&startCmd, "start-cmd", "", "template start command")
	cmd.Flags().StringVar(&readyCmd, "ready-cmd", "", "template ready command")
	cmd.Flags().StringArrayVar(&runSteps, "run", nil, "shell build step; can be repeated")
	cmd.Flags().StringArrayVar(&execSteps, "exec", nil, "exec build step split by whitespace; can be repeated")
	return cmd
}

func newTemplateConvertCommand(apiAddr *string) *cobra.Command {
	var imageID string
	cmd := &cobra.Command{
		Use:   "convert <template_id>",
		Short: "Convert a template to an image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{
				"templateID": args[0],
			}
			if imageID != "" {
				body["imageID"] = imageID
			}
			return requestAndPrint(*apiAddr, http.MethodPost, "/v1/templates/convert", body)
		},
	}
	cmd.Flags().StringVar(&imageID, "image", "", "image id")
	return cmd
}

func newImageCommand(apiAddr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "image",
		Aliases: []string{"img"},
		Short:   "Manage images",
	}

	createCmd := newImageCreateCommand(apiAddr)
	saveCmd := newImageCreateCommand(apiAddr)
	saveCmd.Use = "save <template_id>"
	saveCmd.Short = "Create an image from a template"
	saveCmd.Hidden = true

	cmd.AddCommand(createCmd, saveCmd)
	cmd.AddCommand(listAPICommand(apiAddr, "list", "List images", "/v1/images"))
	cmd.AddCommand(simpleAPICommand(apiAddr, "get <image_id>", "Get image information", http.MethodGet, "/v1/images/%s", 1, nil))
	deleteCmd := simpleAPICommand(apiAddr, "delete <image_id>", "Delete an image", http.MethodDelete, "/v1/images/%s", 1, nil)
	deleteCmd.Aliases = []string{"rm"}
	cmd.AddCommand(deleteCmd)

	return cmd
}

func newImageCreateCommand(apiAddr *string) *cobra.Command {
	var imageID string
	var labels []string
	cmd := &cobra.Command{
		Use:   "create <template_id>",
		Short: "Create an image from a template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{
				"templateID": args[0],
			}
			if imageID != "" {
				body["imageID"] = imageID
			}
			parsedLabels, err := parseKeyValues(labels)
			if err != nil {
				return err
			}
			if len(parsedLabels) > 0 {
				body["labels"] = parsedLabels
			}
			return requestAndPrint(*apiAddr, http.MethodPost, "/v1/images", body)
		},
	}
	cmd.Flags().StringVar(&imageID, "image", "", "image id")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "image label in key=value form; can be repeated")
	return cmd
}

func newRuntimeCommand(apiAddr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Show runtime capabilities",
	}
	cmd.AddCommand(listAPICommand(apiAddr, "list", "List runtimes", "/v1/runtimes"))
	cmd.AddCommand(simpleAPICommand(apiAddr, "show <runtime_type>", "Show runtime information", http.MethodGet, "/v1/runtimes/%s", 1, nil))
	cmd.AddCommand(simpleAPICommand(apiAddr, "capabilities <runtime_type>", "Show runtime capabilities", http.MethodGet, "/v1/runtimes/%s/capabilities", 1, nil))
	return cmd
}

func newSandboxExecCommand(proxyAddr *string) *cobra.Command {
	return newExecCommand(proxyAddr, "exec [-it] <sandbox_id> <cmd> [args...]", "Execute a command in a sandbox")
}

func newExecCommand(proxyAddr *string, use string, short string) *cobra.Command {
	var cwd string
	var attachStdin bool
	var tty bool
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(*proxyAddr, args[0], args[1:], cwd, attachStdin || tty)
		},
	}
	cmd.Flags().BoolVarP(&attachStdin, "interactive", "i", false, "attach stdin")
	cmd.Flags().BoolVarP(&tty, "tty", "t", false, "allocate a terminal")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory inside the sandbox")
	return cmd
}

func newShellCommand(proxyAddr *string) *cobra.Command {
	var shell string
	var cwd string
	cmd := &cobra.Command{
		Use:   "shell <sandbox_id>",
		Short: "Launch an interactive shell in a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(*proxyAddr, args[0], []string{shell}, cwd, true)
		},
	}
	cmd.Flags().StringVar(&shell, "shell", "/bin/sh", "shell executable")
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory inside the sandbox")
	return cmd
}

func simpleAPICommand(apiAddr *string, use string, short string, method string, pathFormat string, argCount int, body any) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(argCount),
		RunE: func(cmd *cobra.Command, args []string) error {
			pathArgs := make([]any, 0, len(args))
			for _, arg := range args {
				pathArgs = append(pathArgs, url.PathEscape(arg))
			}
			path := pathFormat
			if len(pathArgs) > 0 {
				path = fmt.Sprintf(pathFormat, pathArgs...)
			}
			return requestAndPrint(*apiAddr, method, path, body)
		},
	}
}

func listAPICommand(apiAddr *string, use string, short string, path string) *cobra.Command {
	cmd := simpleAPICommand(apiAddr, use, short, http.MethodGet, path, 0, nil)
	cmd.Aliases = []string{"ls"}
	return cmd
}

func requestAndPrint(apiAddr string, method string, path string, body any) error {
	data, err := apiClient{base: apiAddr}.request(method, path, body)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		fmt.Println("ok")
		return nil
	}
	return printRawJSON(data)
}

func (c apiClient) request(method string, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, controlURL(c.base, path), reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp.Status, data)
	}
	return data, nil
}

func controlURL(base string, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func apiError(status string, data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return fmt.Errorf("%s", status)
	}

	var flat struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &flat); err == nil && flat.Message != "" {
		if flat.Code != "" {
			return fmt.Errorf("%s: %s: %s", status, flat.Code, flat.Message)
		}
		return fmt.Errorf("%s: %s", status, flat.Message)
	}

	var nested struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &nested); err == nil && nested.Error.Message != "" {
		if nested.Error.Code != "" {
			return fmt.Errorf("%s: %s: %s", status, nested.Error.Code, nested.Error.Message)
		}
		return fmt.Errorf("%s: %s", status, nested.Error.Message)
	}

	return fmt.Errorf("%s: %s", status, trimmed)
}

func printRawJSON(data []byte) error {
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		fmt.Println(strings.TrimSpace(string(data)))
		return nil
	}
	_, err := out.WriteTo(os.Stdout)
	if err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func printJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return printRawJSON(data)
}

func parseKeyValues(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid key=value pair %q", value)
		}
		out[key] = val
	}
	return out, nil
}

func runExec(proxyAddr string, sandboxID string, command []string, cwd string, interactive bool) error {
	rows, cols := terminalSize()
	startReq := startProcessRequest{
		Cmd:  command,
		Cwd:  cwd,
		TTY:  true,
		Rows: rows,
		Cols: cols,
	}
	body, err := json.Marshal(startReq)
	if err != nil {
		return err
	}

	base := strings.TrimRight(proxyAddr, "/")
	startURL := fmt.Sprintf("%s/processes", base)
	req, err := http.NewRequest(http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build start process request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Novita-Sandbox-Id", sandboxID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("start process failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var startResp startProcessResponse
	if err := json.NewDecoder(resp.Body).Decode(&startResp); err != nil {
		return fmt.Errorf("decode start process response: %w", err)
	}
	if startResp.Process.ID == "" {
		return fmt.Errorf("start process response missing process id")
	}

	wsURL, err := processWebSocketURL(base, sandboxID, startResp.Process.ID)
	if err != nil {
		return err
	}
	origin := "http://127.0.0.1"
	conn, err := websocket.Dial(wsURL, "", origin)
	if err != nil {
		return fmt.Errorf("connect process: %w", err)
	}
	defer conn.Close()
	var startFrame []byte
	_ = websocket.Message.Receive(conn, &startFrame)

	var restore func() error
	if interactive {
		restore, err = makeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("set terminal raw mode: %w", err)
		}
		defer restore()
		go resizeLoop(base, sandboxID, startResp.Process.ID)
	}

	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(os.Stdout, conn)
		errCh <- err
	}()
	err = <-errCh
	if err != nil && !isClosed(err) {
		return err
	}
	return nil
}

func processWebSocketURL(base string, sandboxID string, processID string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	u.Path = fmt.Sprintf("/processes/%s/connect", url.PathEscape(processID))
	q := u.Query()
	q.Set("sandboxID", sandboxID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func resizeLoop(base string, sandboxID string, processID string) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)
	for {
		<-ch
		rows, cols := terminalSize()
		payload, _ := json.Marshal(map[string]uint16{"rows": rows, "cols": cols})
		req, err := http.NewRequest(
			http.MethodPost,
			fmt.Sprintf("%s/processes/%s/resize", strings.TrimRight(base, "/"), url.PathEscape(processID)),
			bytes.NewReader(payload),
		)
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Novita-Sandbox-Id", sandboxID)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}

func terminalSize() (uint16, uint16) {
	return getTerminalSize(int(os.Stdout.Fd()))
}

func isClosed(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "use of closed network connection") ||
		strings.Contains(text, "websocket: close") ||
		strings.Contains(text, "EOF") ||
		strings.Contains(text, "i/o timeout") ||
		strings.Contains(text, "broken pipe")
}

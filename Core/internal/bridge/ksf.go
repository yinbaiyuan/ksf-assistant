package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"ksfassistant/core/internal/domain"
)

type KSFClient struct{}

func (KSFClient) Enable(ctx context.Context, root string) (time.Time, error) {
	data, err := runKSF(ctx, root, []string{"--enable"}, nil)
	if err != nil {
		return time.Time{}, err
	}
	var response struct {
		Enabled   bool   `json:"enabled"`
		EnabledAt string `json:"enabledAt"`
	}
	if json.Unmarshal(data, &response) != nil || !response.Enabled {
		return time.Time{}, fmt.Errorf("KSF 面板桥接返回了无法识别的数据")
	}
	return time.Parse(time.RFC3339Nano, response.EnabledAt)
}

func (KSFClient) Catalog(ctx context.Context, root string) (domain.ProjectCatalog, error) {
	data, err := runKSF(ctx, root, []string{"--export-catalog"}, nil)
	if err != nil {
		return domain.ProjectCatalog{}, err
	}
	var response domain.ProjectCatalog
	if json.Unmarshal(data, &response) != nil || response.Protocol != "ksf-panel-catalog-v1" {
		return domain.ProjectCatalog{}, fmt.Errorf("KSF 面板桥接返回了无法识别的数据")
	}
	return response, nil
}

func (KSFClient) Projections(ctx context.Context, root string, threadIDs []string) (map[string]domain.TaskProjection, error) {
	if len(threadIDs) == 0 {
		return map[string]domain.TaskProjection{}, nil
	}
	input, _ := json.Marshal(map[string]any{"threadIds": threadIDs})
	data, err := runKSF(ctx, root, []string{"--resolve-projections"}, input)
	if err != nil {
		return nil, err
	}
	var response domain.ProjectionResolution
	if json.Unmarshal(data, &response) != nil || response.Protocol != "ksf-task-project-resolution-v1" {
		return nil, fmt.Errorf("KSF 任务投影返回了无法识别的数据")
	}
	result := map[string]domain.TaskProjection{}
	for _, value := range response.Projections {
		result[value.ThreadID] = value.Projection
	}
	return result, nil
}

func runKSF(ctx context.Context, root string, arguments []string, input []byte) ([]byte, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(filepath.Join(root, "AGENTS.md")); err != nil || info.IsDir() {
		return nil, fmt.Errorf("所选目录不是有效的 KSF 根目录：缺少 AGENTS.md")
	}
	script := filepath.Join(root, ".agents", "skills", "ksf-load-route-context", "scripts", "ksf_panel_bridge.rb")
	info, err := os.Lstat(script)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("KSF 面板桥接脚本不存在或不安全")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("KSF 面板桥接脚本不可被组或其他用户写入")
	}
	ruby, err := exec.LookPath("ruby")
	if err != nil && runtime.GOOS == "windows" {
		ruby, err = exec.LookPath("ruby.exe")
	}
	if err != nil {
		return nil, fmt.Errorf("未找到 Ruby，无法运行 KSF 面板桥接")
	}
	args := append([]string{script, "--root", root}, arguments...)
	return run(ctx, root, ruby, args, input)
}

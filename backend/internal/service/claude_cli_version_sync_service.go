package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

const (
	// claudeCLIVersionSyncInterval 自动同步间隔，与 OpenAI Codex 侧一致：客户端发版频率是
	// 天级，6 小时足够及时跟上，同时把对 GitHub API 的调用压到每天 4 次。
	claudeCLIVersionSyncInterval = 6 * time.Hour
	// claudeCLIVersionSyncTimeout 单次同步的整体超时。
	claudeCLIVersionSyncTimeout = 30 * time.Second
	// claudeCLIVersionSyncRepo 官方 Claude Code CLI 客户端仓库。
	claudeCLIVersionSyncRepo = "anthropics/claude-code"
	// claudeCLIVersionSyncPerPage 回退路径单次拉取的 release 数量。这个仓库只发布这一个
	// 客户端组件、tag 干净（v2.1.280 形态，无预发布噪音），远不像 openai/codex 那样需要
	// 大页扫描；保留一个适中的页大小仅作为 /releases/latest 失败时的兜底。
	claudeCLIVersionSyncPerPage = 10
	// claudeCLIVersionTagPrefix 客户端 release 的 tag 前缀（如 v2.1.280）。
	claudeCLIVersionTagPrefix = "v"
)

// ClaudeCLIVersionSyncService 周期性把官方 Claude Code CLI 客户端的最新稳定版版本号同步到
// 设置，供出站身份伪装使用（Anthropic 对新模型设客户端版本下限，如 Opus 5.5 要求
// claude-cli >= 2.1.280），避免每次都要靠运维发现报错、手动设置
// SUB2API_CLAUDE_CLI_VERSION 才能跟上。
//
// 同步值写入 SettingKeyClaudeCLIClientVersionSynced（本服务独占写入）；
// SUB2API_CLAUDE_CLI_VERSION 环境变量优先级更高，因此手工固定版本不会被同步覆盖——
// 详见 claude.SetVersionOverride 与其在启动阶段的调用点（ProvideClaudeCLIVersionSyncService）。
//
// 与 OpenAICodexVersionSyncService 的关键差异：出站 Codex 身份可以按请求实时读取当前设置
// （60s 缓存），而 claude-cli 出站身份（User-Agent 与请求体 cc_version）必须在一个进程
// 生命周期内保持恒定，两者由不同代码路径写入，一旦不一致就会被上游判定为非正版客户端
// （见 internal/pkg/claude/cli_version.go 的注释）。因此本服务只负责把发现的版本号写进
// 设置；真正生效需要等到下一次 sub2api 进程启动时读取该设置并调用一次
// claude.SetVersionOverride——本服务自身运行期间不会让正在运行的进程切换出站版本。
type ClaudeCLIVersionSyncService struct {
	settingRepo  SettingRepository
	githubClient GitHubReleaseClient
	interval     time.Duration
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
}

func NewClaudeCLIVersionSyncService(
	settingRepo SettingRepository,
	githubClient GitHubReleaseClient,
	interval time.Duration,
) *ClaudeCLIVersionSyncService {
	return &ClaudeCLIVersionSyncService{
		settingRepo:  settingRepo,
		githubClient: githubClient,
		interval:     interval,
		stopCh:       make(chan struct{}),
	}
}

func (s *ClaudeCLIVersionSyncService) Start() {
	if s == nil || s.settingRepo == nil || s.githubClient == nil || s.interval <= 0 {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		s.runInitial()
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *ClaudeCLIVersionSyncService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.wg.Wait()
}

// runInitial 执行启动时的首次同步。若同步值在一个同步周期内已被刷新过则跳过：频繁重启、
// 滚动发布或崩溃重启会让「启动即同步」放大成对 GitHub 的连续请求，而版本号是天级变化的，
// 重启后没有立刻重新拉取的必要。
func (s *ClaudeCLIVersionSyncService) runInitial() {
	if s.syncedWithinInterval() {
		return
	}
	s.runOnce()
}

// syncedWithinInterval 判断已同步值是否仍在一个同步周期内。借设置行自身的 UpdatedAt 判断，
// 无需额外记录时间戳的设置项。读取失败或尚无有效同步值时返回 false，让启动同步照常执行。
func (s *ClaudeCLIVersionSyncService) syncedWithinInterval() bool {
	if s.interval <= 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), claudeCLIVersionSyncTimeout)
	defer cancel()

	setting, err := s.settingRepo.Get(ctx, SettingKeyClaudeCLIClientVersionSynced)
	if err != nil || setting == nil || setting.UpdatedAt.IsZero() {
		return false
	}
	if !claude.IsSupportedCLIVersion(setting.Value) {
		return false
	}
	return time.Since(setting.UpdatedAt) < s.interval
}

func (s *ClaudeCLIVersionSyncService) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), claudeCLIVersionSyncTimeout)
	defer cancel()

	if !s.autoSyncEnabled(ctx) {
		return
	}

	latest := s.fetchLatestStableVersion(ctx)
	if latest == "" {
		return
	}

	current := s.currentSyncedVersion(ctx)
	// 只向前推进：上游偶发返回旧数据或重新发布历史 tag 时不把已同步的版本号降级。
	if current != "" && claude.IsSupportedCLIVersion(current) && CompareVersions(latest, current) <= 0 {
		return
	}
	if err := s.settingRepo.Set(ctx, SettingKeyClaudeCLIClientVersionSynced, latest); err != nil {
		slog.Warn("claude_cli_version_sync_persist_failed", "version", latest, "error", err)
		return
	}
	slog.Info("claude_cli_version_synced", "previous", current, "version", latest)
}

// fetchLatestStableVersion 取官方最新稳定版客户端版本号；取不到时返回空串，由调用方保持
// 既有值（不清空、不降级），各失败分支自行落日志。
//
// 主路径 /releases/latest：该端点本身就排除 draft 与 prerelease，直接给出最新正式发布。
// 回退列表扫描：仅当主路径失败时使用，语义与主路径一致（同一套过滤）。
func (s *ClaudeCLIVersionSyncService) fetchLatestStableVersion(ctx context.Context) string {
	release, err := s.githubClient.FetchLatestRelease(ctx, claudeCLIVersionSyncRepo)
	if err != nil {
		slog.Warn("claude_cli_version_sync_latest_fetch_failed", "error", err)
	} else if version := latestClaudeCLIStableReleaseVersion([]*GitHubRelease{release}); version != "" {
		return version
	}

	releases, err := s.githubClient.FetchRecentReleases(ctx, claudeCLIVersionSyncRepo, claudeCLIVersionSyncPerPage)
	if err != nil {
		slog.Warn("claude_cli_version_sync_fetch_failed", "error", err)
		return ""
	}
	version := latestClaudeCLIStableReleaseVersion(releases)
	if version == "" {
		slog.Warn("claude_cli_version_sync_no_stable_release", "repo", claudeCLIVersionSyncRepo)
	}
	return version
}

// autoSyncEnabled 读取开关。缺失或空值视为开启；读取失败时保持开启，避免一次数据库抖动就
// 静默停掉版本跟随。目前没有面板 UI，需要关闭时直接写这个 setting key 为 "false"。
func (s *ClaudeCLIVersionSyncService) autoSyncEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyClaudeCLIVersionAutoSyncEnabled)
	if err != nil {
		return true
	}
	if strings.TrimSpace(value) == "" {
		return true
	}
	return strings.TrimSpace(value) == "true"
}

func (s *ClaudeCLIVersionSyncService) currentSyncedVersion(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyClaudeCLIClientVersionSynced)
	if err != nil {
		return ""
	}
	return value
}

// latestClaudeCLIStableReleaseVersion 从 release 列表里挑出最大的稳定版客户端版本号。
// 过滤条件：tag 前缀为 v、非草稿、非预发布、版本号是严格三段纯数字（拒绝 -alpha/-beta 等
// 后缀，与 claude.IsSupportedCLIVersion 的校验口径一致）。取最大值而非最新发布，避免
// 重新发布历史 tag 造成回退。
func latestClaudeCLIStableReleaseVersion(releases []*GitHubRelease) string {
	best := ""
	for _, release := range releases {
		if release == nil || release.Draft || release.Prerelease {
			continue
		}
		tag := strings.TrimSpace(release.TagName)
		if !strings.HasPrefix(tag, claudeCLIVersionTagPrefix) {
			continue
		}
		version := strings.TrimPrefix(tag, claudeCLIVersionTagPrefix)
		if !claude.IsSupportedCLIVersion(version) {
			continue
		}
		if best == "" || CompareVersions(version, best) > 0 {
			best = version
		}
	}
	return best
}

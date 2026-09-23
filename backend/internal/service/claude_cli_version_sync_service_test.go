package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Reuses codexVersionSyncSettingRepoStub / codexVersionSyncGitHubStub from
// openai_codex_version_sync_service_test.go (same package, generic enough
// for both syncers).

func newClaudeCLIVersionSyncTestService(
	repo SettingRepository,
	github GitHubReleaseClient,
) *ClaudeCLIVersionSyncService {
	return NewClaudeCLIVersionSyncService(repo, github, claudeCLIVersionSyncInterval)
}

// anthropics/claude-code only ships this one client component with clean
// v-prefixed tags (no rust-v-style noise from unrelated components), so the
// filter is simpler than the Codex one, but must still reject drafts,
// prereleases, and malformed version suffixes.
func TestLatestClaudeCLIStableReleaseVersion(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v2.1.280"},
		{TagName: "v2.1.281-beta.1", Prerelease: true},
		{TagName: "v2.1.279", Draft: true},
		{TagName: "not-a-tag"},
		{TagName: "v2.1.278"},
		nil,
	}

	require.Equal(t, "2.1.280", latestClaudeCLIStableReleaseVersion(releases))
	require.Empty(t, latestClaudeCLIStableReleaseVersion(nil))
	require.Empty(t, latestClaudeCLIStableReleaseVersion([]*GitHubRelease{{TagName: "not-a-tag"}}))
	require.Empty(t, latestClaudeCLIStableReleaseVersion([]*GitHubRelease{{TagName: "v2.1.281-beta.1", Prerelease: true}}))
}

func TestClaudeCLIVersionSyncWritesLatestStableVersion(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(nil)
	github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{
		{TagName: "v2.1.278"},
		{TagName: "v2.1.280"},
	}}

	newClaudeCLIVersionSyncTestService(repo, github).runOnce()

	require.Equal(t, []string{"2.1.280"}, repo.syncedWrites())
}

// 只向前推进：上游偶发返回旧数据或重新发布历史 tag 时不把已同步版本降级。
func TestClaudeCLIVersionSyncNeverMovesBackwards(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(map[string]string{
		SettingKeyClaudeCLIClientVersionSynced: "2.1.280",
	})
	github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "v2.1.278"}}}

	newClaudeCLIVersionSyncTestService(repo, github).runOnce()

	require.Empty(t, repo.syncedWrites())
}

func TestClaudeCLIVersionSyncSkippedWhenDisabled(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(map[string]string{
		SettingKeyClaudeCLIVersionAutoSyncEnabled: "false",
	})
	github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "v2.1.280"}}}

	newClaudeCLIVersionSyncTestService(repo, github).runOnce()

	require.Zero(t, github.latestCalls, "关闭自动同步后不应请求上游")
	require.Zero(t, github.calls, "关闭自动同步后不应请求上游")
	require.Empty(t, repo.syncedWrites())
}

// 面板开关缺失或为空一律视为开启，与设置默认值一致。
func TestClaudeCLIVersionSyncEnabledByDefault(t *testing.T) {
	for _, value := range []string{"", "true"} {
		repo := newCodexVersionSyncSettingRepoStub(map[string]string{
			SettingKeyClaudeCLIVersionAutoSyncEnabled: value,
		})
		github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "v2.1.280"}}}

		newClaudeCLIVersionSyncTestService(repo, github).runOnce()

		require.Equal(t, []string{"2.1.280"}, repo.syncedWrites(), "开关值 %q", value)
	}
}

// 抓取失败保持既有值，不清空、不降级。
func TestClaudeCLIVersionSyncKeepsValueOnFetchError(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(map[string]string{
		SettingKeyClaudeCLIClientVersionSynced: "2.1.280",
	})
	github := &codexVersionSyncGitHubStub{
		latestErr: errors.New("network down"),
		err:       errors.New("network down"),
	}

	newClaudeCLIVersionSyncTestService(repo, github).runOnce()

	require.Empty(t, repo.syncedWrites())
	value, err := repo.GetValue(context.Background(), SettingKeyClaudeCLIClientVersionSynced)
	require.NoError(t, err)
	require.Equal(t, "2.1.280", value)
}

// 主路径 /releases/latest 可用时不应再拉列表页。
func TestClaudeCLIVersionSyncUsesLatestReleaseEndpoint(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(nil)
	github := &codexVersionSyncGitHubStub{
		latest:   &GitHubRelease{TagName: "v2.1.280"},
		releases: []*GitHubRelease{{TagName: "v2.1.278"}},
	}

	newClaudeCLIVersionSyncTestService(repo, github).runOnce()

	require.Equal(t, 1, github.latestCalls)
	require.Zero(t, github.calls, "主路径可用时不应再拉列表页")
	require.Equal(t, []string{"2.1.280"}, repo.syncedWrites())
}

// 主路径拿不到可用版本时（预发布/草稿/抓取失败）必须回退列表扫描，否则版本号会静默停更。
func TestClaudeCLIVersionSyncFallsBackToReleaseList(t *testing.T) {
	tests := []struct {
		name      string
		latest    *GitHubRelease
		latestErr error
	}{
		{name: "latest 是预发布", latest: &GitHubRelease{TagName: "v2.1.281-beta.1", Prerelease: true}},
		{name: "latest 抓取失败", latestErr: errors.New("network down")},
		{name: "latest 为空", latest: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newCodexVersionSyncSettingRepoStub(nil)
			github := &codexVersionSyncGitHubStub{
				latest:    tt.latest,
				latestErr: tt.latestErr,
				releases: []*GitHubRelease{
					{TagName: "v2.1.281-beta.1", Prerelease: true},
					{TagName: "v2.1.280"},
					{TagName: "v2.1.278"},
				},
			}

			newClaudeCLIVersionSyncTestService(repo, github).runOnce()

			require.Equal(t, 1, github.latestCalls)
			require.Equal(t, 1, github.calls)
			require.Equal(t, []string{"2.1.280"}, repo.syncedWrites())
		})
	}
}

// 依赖缺失时 Start 必须直接返回，不能起一个空转的 goroutine。
func TestClaudeCLIVersionSyncStartRequiresDependencies(t *testing.T) {
	require.NotPanics(t, func() {
		svc := NewClaudeCLIVersionSyncService(nil, nil, claudeCLIVersionSyncInterval)
		svc.Start()
		svc.Stop()
	})
}

// 启动同步防抖：同步值仍在一个周期内时跳过。
func TestClaudeCLIVersionSyncInitialSkipsWhenRecentlySynced(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(map[string]string{
		SettingKeyClaudeCLIClientVersionSynced: "2.1.278",
	})
	repo.updatedAt = time.Now().Add(-time.Hour)
	github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "v2.1.280"}}}

	newClaudeCLIVersionSyncTestService(repo, github).runInitial()

	require.Zero(t, github.calls, "同步值仍在周期内时不应请求上游")
	require.Empty(t, repo.syncedWrites())
}

func TestClaudeCLIVersionSyncInitialRunsWhenStaleOrMissing(t *testing.T) {
	t.Run("同步值已过期", func(t *testing.T) {
		repo := newCodexVersionSyncSettingRepoStub(map[string]string{
			SettingKeyClaudeCLIClientVersionSynced: "2.1.278",
		})
		repo.updatedAt = time.Now().Add(-7 * time.Hour)
		github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "v2.1.280"}}}

		newClaudeCLIVersionSyncTestService(repo, github).runInitial()

		require.Equal(t, []string{"2.1.280"}, repo.syncedWrites())
	})

	t.Run("尚无同步值", func(t *testing.T) {
		repo := newCodexVersionSyncSettingRepoStub(nil)
		repo.updatedAt = time.Now()
		github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "v2.1.280"}}}

		newClaudeCLIVersionSyncTestService(repo, github).runInitial()

		require.Equal(t, []string{"2.1.280"}, repo.syncedWrites())
	})
}

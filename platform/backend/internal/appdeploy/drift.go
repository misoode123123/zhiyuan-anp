package appdeploy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// imageVersionRe 从镜像引用 appdeploy/<slug>-<env>:v<N> 提取尾部版本号 N。
var imageVersionRe = regexp.MustCompile(`:v(\d+)$`)

// parseImageVersion 解析镜像 tag 尾部的 v<N> 为版本号；非数字 tag（如 latest）返回 (0,false)。
func parseImageVersion(image string) (int, bool) {
	m := imageVersionRe.FindStringSubmatch(image)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// DriftResult 三方（DB 记录 ↔ 运行容器 ↔ deploy.yaml actual）镜像一致性比对结果。
type DriftResult struct {
	OK           bool   `json:"ok"`
	DBImage      string `json:"db_image,omitempty"`
	ContainerImg string `json:"container_image,omitempty"`
	ManifestImg  string `json:"manifest_image,omitempty"`
	Reason       string `json:"reason,omitempty"` // 不一致时的人话描述（哪几方分叉）
}

// checkDrift 比对 DB 镜像 ↔ 容器镜像 ↔ manifest actual 镜像。纯函数。
// containerImg 为空时退化为 DB↔manifest 两方比（部署时回读失败 / Stats 容器查不到）。
// 全空或仅一个非空源 → OK（信息不足不判漂移）；两个以上非空源且不全等 → 不 OK + Reason。
//
// 注：只比「非空」对，跳过空源——空源（如无 manifest 的 legacy 应用）不参与判决，
// 避免把「无 deploy.yaml」误报为漂移。
func checkDrift(dbImage, containerImg, manifestImg string) DriftResult {
	r := DriftResult{DBImage: dbImage, ContainerImg: containerImg, ManifestImg: manifestImg}
	var diffs []string
	if dbImage != "" && containerImg != "" && dbImage != containerImg {
		diffs = append(diffs, fmt.Sprintf("DB记录 %q ≠ 运行容器 %q", dbImage, containerImg))
	}
	if dbImage != "" && manifestImg != "" && dbImage != manifestImg {
		diffs = append(diffs, fmt.Sprintf("DB记录 %q ≠ deploy.yaml actual %q", dbImage, manifestImg))
	}
	if containerImg != "" && manifestImg != "" && containerImg != manifestImg {
		diffs = append(diffs, fmt.Sprintf("运行容器 %q ≠ deploy.yaml actual %q", containerImg, manifestImg))
	}
	if len(diffs) == 0 {
		r.OK = true
		return r
	}
	r.Reason = strings.Join(diffs, "; ")
	return r
}

// highWaterMarkVersion 把 DB 计数器提到 max(cur, 容器版本号)，**只升不降**。
// 向下（容器比记录旧=疑似回滚）不纠正——回退计数器会导致下次 Build 复用版本号 → 镜像 tag
// 碰撞 / 审计混乱。changed=false 表示无需更新（容器更旧 / 等于 / tag 无法解析）。
func highWaterMarkVersion(cur int, containerImg string) (newVer int, changed bool) {
	cv, ok := parseImageVersion(containerImg)
	if !ok || cv <= cur {
		return cur, false
	}
	return cv, true
}

// reconcileActual 把 deploy.yaml actual.image_digest 对齐到运行容器镜像（安全自愈）。
// actual 仅是确定性重放的缓存记录，覆写 ImageDigest 无副作用；**不动** HostPort/MountsSrc/
// EngineVersion（确定性重放依赖它们）+ 不动 Needs（opencode 维护）。
// mf nil / containerImg 空 / 已一致 → false（no-op）；否则改 ImageDigest 并写回，返回是否写成功。
func reconcileActual(repoDir string, mf *DeployManifest, containerImg string) bool {
	if mf == nil || containerImg == "" || mf.Actual.ImageDigest == containerImg {
		return false
	}
	mf.Actual.ImageDigest = containerImg
	return WriteDeployManifest(repoDir, mf) == nil
}

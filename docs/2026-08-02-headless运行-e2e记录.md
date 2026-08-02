# headless 应用运行态 — .28 端到端验证记录

> 日期:2026-08-02 ｜ 对应 spec `docs/superpowers/specs/2026-08-02-headless运行-design.md`、plan `docs/superpowers/plans/2026-08-02-headless运行.md`
> 分支:main(944ca89..6de3d74,11 commit)。部署:.28 prod 库 anp(deploy_postgres_1),backend/frontend 重建。

## 部署链路(按 memory deploy-prod-10.10.0.28)

- `git push origin main` → tar(排除 data/deploy/.env.prod/deploy/docker-compose.prod.yml)→ scp .28:/root → `cd /opt/anp && tar -xzf`。
- `docker-compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build backend` → `up -d --build frontend`(后台 nohup)。
- 三查(防假成功):backend 容器 CreatedAt=新建、`schema_migrations` 含 `000032_appdeploy_headless_health`、二进制含新串 `apphealth`(OpsHealthAlerter source)。`healthz={"status":"ok"}`。

## 验收结论(spec §14 逐条)

| #   | 验收项                        | 结果          | 实测证据(.28 anp 库)                                                                                                             |
| --- | ----------------------------- | ------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| 1   | headless 部署无端口/无 URL    | ✅            | 容器 `appdeploy-headless-e2e-test-v1` Ports=`80/tcp=[] 8080/tcp=[]`(EXPOSE 未发布);instance `url='' host_port=0 status=running`  |
| 2   | 容器崩溃→failed+critical 告警 | ✅            | `docker stop` 后 reconcile 翻 `failed` last_error="容器退出(exit=0)";ops_alert `apphealth/critical/firing`                       |
| 3   | 恢复→running+resolved         | ✅(修后)      | 见下「e2e 发现的 bug」;修后 failed 实例自动翻 `running` last_error 清空,告警 `resolved`                                          |
| 4   | crash-loop→degraded+warning   | ✅            | 杀 nginx 拉高 RestartCount,ops_alert 出 `warning`(13:11:57 fired→13:12:27 resolved),证明 degraded 路径触发(单周期 delta≥burst=3) |
| 5   | restart_count 列回写正确      | ✅            | instance.restart_count 随 docker RestartCount:0→5→8,reconcile 每轮更新基线                                                       |
| 6   | inspect 失败不误判            | ✅(设计)      | checkOne inspect-fail 路径保留原 status 只记 last_error(单测 TestCheckOne_InspectFail_NoFlip)                                    |
| 7   | web/service 全链路不变        | ✅            | 无 web/service 实例被 reconcile 标 failed/degraded;需求申请单/ncc_deploy/客服机器人 仍 running,端口映射不变                      |
| 8   | 详情页健康徽标渲染            | ✅(代码+构建) | page.tsx 17 处 headless + healthBadge;构建产物 .next chunk 含 headless;8088=200。**浏览器肉眼渲染待用户确认**                    |

## e2e 发现的 bug(已修)

**reconcile 漏巡 failed 实例 → 崩溃后无法自动恢复**(acceptance #3 不通过):

- 现象:`docker stop`→failed+critical 告警(✅),但 `docker start` 恢复后 status 卡 failed 不回 running、告警不 resolve。
- 根因:`ListHeadlessActiveInstances` 查询 `status IN ('running','degraded')`,**排除 failed** → 一旦 failed, reconcile 不再巡它 → 容器恢复也无人翻 running。checkOne 本身能翻 failed→running(单测证),但生产循环根本不查 failed 实例。
- 修复(commit `6de3d74`):查询改 `status IN ('running','degraded','failed')`。failed 实例若容器真死则保持 failed(无翻转无重报);若恢复则→running+resolve。补 store 测试(failed 实例被纳入)。重部署后 #3 通过。

## 告警史(.28 anp.ops_alert,source=apphealth)

```
critical|resolved|13:01:27→13:09:27  (首次 docker stop→failed;redeploy+fix 后恢复)
critical|resolved|13:10:27→13:10:57  (杀进程瞬时退出→failed;30s 内恢复)
warning |resolved|13:11:57→13:12:27  (crash-loop→degraded;重启停止后恢复)
```

三种状态(failed/critical、degraded/warning、running/resolve)全覆盖,去重+恢复均生效。

## 备注 / 已知限制

- 测试 app 用 nginx 镜像(buildpack 生成,覆盖了仓库里的 alpine Dockerfile——buildpack 行为,非 headless 缺陷)。nginx 作为长驻进程足够验证 reconcile;真实 headless 应自带跑 worker 的 Dockerfile。
- crash-loop degraded 未在「instance status 轮询」里当场抓到 degraded 瞬态(docker 重启 backoff 把重启摊到多周期),但 warning 告警 fired/resolved 证明 degraded 路径生效;逻辑另有单测(TestAggregateHealth + TestCheckOne phase3)直证。
- 前端浏览器肉眼渲染(健康徽标颜色/文案)是唯一未自主验证项,待用户开 `http://10.10.0.28:8088` 看 headless-e2e 应用详情页确认。测试 app `headless-e2e`(ps_default)保留供查看,验完可删。

// devStep 纯函数测试（不依赖框架，tsx 直接跑）。
//   跑法: npx tsx platform/frontend/lib/devstep.test.ts
import { devStep } from "./devstep";

const I = (image: string | undefined, instances: { env: string; status: string }[] | undefined) =>
  ({ image, instances });

const cases: { name: string; input: any; current: string; hint: string }[] = [
  { name: "全新无镜像", input: I("", []), current: "code", hint: "先打开编码工作台写代码，再构建部署" },
  { name: "有镜像无 test", input: I("img:v1", []), current: "test", hint: "代码已构建，点「构建部署(test)」验证" },
  { name: "test running 无 prod", input: I("img:v1", [{ env: "test", status: "running" }]), current: "prod", hint: "test 已跑通，点「🚀上线」到 prod" },
  { name: "prod running 全绿", input: I("img:v1", [{ env: "prod", status: "running" }]), current: "done", hint: "已上线 ✅，可继续编码迭代" },
  { name: "test 非 running 不算完成", input: I("img:v1", [{ env: "test", status: "building" }]), current: "test", hint: "代码已构建，点「构建部署(test)」验证" },
  { name: "test stopped 不算完成", input: I("img:v1", [{ env: "test", status: "stopped" }]), current: "test", hint: "代码已构建，点「构建部署(test)」验证" },
];

let fail = 0;
for (const c of cases) {
  const g = devStep(c.input);
  if (g.current !== c.current || g.hint !== c.hint) {
    console.error(`FAIL [${c.name}] current=${g.current}(want ${c.current}) hint="${g.hint}"`);
    fail++;
  } else {
    console.log(`ok   [${c.name}] -> ${g.current}`);
  }
}
console.log(fail === 0 ? "ALL PASS" : `${fail} FAILED`);
process.exit(fail === 0 ? 0 : 1);

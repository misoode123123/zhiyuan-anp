import Link from "next/link";

const NAV = [
  { href: "/", label: "概览", icon: "📊" },
  { href: "/requirements", label: "需求工作台", icon: "💬" },
  { href: "/dev", label: "研发工作台", icon: "💻" },
  { href: "/testing", label: "测试中心", icon: "🧪" },
  { href: "/release", label: "发布中心", icon: "🚀" },
  { href: "/ops", label: "运维中心", icon: "🛠️" },
  { href: "/governance", label: "规则治理", icon: "⭐" },
  { href: "/security", label: "安全合规", icon: "🛡️" },
  { href: "/docs", label: "方案文档", icon: "📄" },
  { href: "/compute", label: "算力资源", icon: "⚡" },
  { href: "/admin/config", label: "系统配置", icon: "⚙️" },
  { href: "/admin/users", label: "用户权限", icon: "🔐" },
  { href: "/approvals", label: "变更审批", icon: "🚪" },
];

export function Sidebar() {
  return (
    <nav className="flex flex-col gap-0.5">
      {NAV.map((item) => (
        <Link
          key={item.href}
          href={item.href}
          className="rounded-md px-3 py-2 text-sm text-neutral-700 hover:bg-neutral-100 transition-colors"
        >
          <span className="mr-2">{item.icon}</span>
          {item.label}
        </Link>
      ))}
    </nav>
  );
}

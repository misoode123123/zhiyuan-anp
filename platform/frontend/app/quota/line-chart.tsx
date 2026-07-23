"use client";

// LineChart 极简 SVG 折线图（用量看板3c，不引图表库）。
// 自研风格：viewBox 自适应宽度；y 轴标 min/max；x 轴标首尾；点 hover 原生 title。
// 空数据渲染占位，由调用方决定标题/摘要。

export type ChartPoint = { label: string; value: number };

export function LineChart({
  data,
  color = "#3b82f6",
  height = 160,
  unit = "",
}: {
  data: ChartPoint[];
  color?: string;
  height?: number;
  unit?: string;
}) {
  if (!data || data.length === 0) {
    return (
      <div className="flex items-center justify-center text-sm text-neutral-400" style={{ height }}>
        暂无数据
      </div>
    );
  }

  const W = 640;
  const H = height;
  const padL = 52;
  const padR = 16;
  const padT = 12;
  const padB = 22;
  const innerW = W - padL - padR;
  const innerH = H - padT - padB;

  const values = data.map((d) => d.value);
  const max = Math.max(...values, 1);
  const min = Math.min(...values, 0);
  const range = max - min || 1;

  const x = (i: number) =>
    padL + (data.length === 1 ? innerW / 2 : (i / (data.length - 1)) * innerW);
  const y = (v: number) => padT + innerH - ((v - min) / range) * innerH;

  const path = data
    .map((d, i) => `${i === 0 ? "M" : "L"} ${x(i).toFixed(1)} ${y(d.value).toFixed(1)}`)
    .join(" ");

  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      className="w-full"
      style={{ maxHeight: H }}
      preserveAspectRatio="xMidYMid meet"
      role="img"
    >
      {/* 网格线：顶 max / 底 min */}
      <line x1={padL} y1={padT} x2={W - padR} y2={padT} stroke="#e5e5e5" strokeWidth={1} />
      <line
        x1={padL}
        y1={padT + innerH}
        x2={W - padR}
        y2={padT + innerH}
        stroke="#d4d4d4"
        strokeWidth={1}
      />
      <text x={padL - 6} y={padT + 4} textAnchor="end" fontSize={10} fill="#737373">
        {fmt(max)}
        {unit}
      </text>
      <text x={padL - 6} y={padT + innerH + 4} textAnchor="end" fontSize={10} fill="#737373">
        {fmt(min)}
        {unit}
      </text>

      {/* x 轴：首/尾日期 */}
      <text x={x(0)} y={H - 6} textAnchor="middle" fontSize={10} fill="#737373">
        {data[0].label}
      </text>
      {data.length > 1 && (
        <text x={x(data.length - 1)} y={H - 6} textAnchor="middle" fontSize={10} fill="#737373">
          {data[data.length - 1].label}
        </text>
      )}

      {/* 折线 + 点（hover 原生 title 显示日期+值） */}
      <path d={path} fill="none" stroke={color} strokeWidth={2} strokeLinejoin="round" />
      {data.map((d, i) => (
        <circle key={i} cx={x(i)} cy={y(d.value)} r={3} fill={color}>
          <title>{`${d.label}: ${fmt(d.value)}${unit}`}</title>
        </circle>
      ))}
    </svg>
  );
}

// fmt 量级缩写（1000→1.0K，1000000→1.0M），避免 y 轴长数字挤压。
export function fmt(n: number): string {
  if (Math.abs(n) >= 1e9) return (n / 1e9).toFixed(1) + "B";
  if (Math.abs(n) >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (Math.abs(n) >= 1e3) return (n / 1e3).toFixed(1) + "K";
  return String(Math.round(n));
}

function extent(values: number[]): [number, number] {
  let min = values[0]
  let max = values[0]
  for (const v of values) {
    if (v < min) min = v
    if (v > max) max = v
  }
  return [min, max]
}

export function Sparkline({
  values,
  width = 120,
  height = 32,
  stroke = 'var(--g-color-text-brand)',
}: {
  values: number[]
  width?: number
  height?: number
  stroke?: string
}) {
  if (!values || values.length < 2) {
    return <svg width={width} height={height} aria-label="нет данных" role="img" />
  }
  const [min, max] = extent(values)
  const range = max - min || 1
  const stepX = width / (values.length - 1)
  const points = values.map((v, i) => {
    const x = i * stepX
    const y = height - 3 - ((v - min) / range) * (height - 6)
    return `${x.toFixed(1)},${y.toFixed(1)}`
  })
  return (
    <svg width={width} height={height} aria-label="динамика баллов" role="img">
      <polyline
        points={points.join(' ')}
        fill="none"
        strokeWidth="1.5"
        strokeLinejoin="round"
        strokeLinecap="round"
        style={{ stroke }}
      />
    </svg>
  )
}

export function LineChart({
  values,
  width = 640,
  height = 180,
}: {
  values: { score: number; captured_at: string }[]
  width?: number
  height?: number
}) {
  if (!values || values.length < 2) {
    return <span className="hs-chart-empty">Истории пока недостаточно</span>
  }
  const scores = values.map((v) => v.score)
  const [min, max] = extent(scores)
  const range = max - min || 1
  const pad = 24
  const stepX = (width - pad * 2) / (values.length - 1)
  const points = values.map((v, i) => {
    const x = pad + i * stepX
    const y = height - pad - ((v.score - min) / range) * (height - pad * 2)
    return `${x.toFixed(1)},${y.toFixed(1)}`
  })
  const last = values[values.length - 1]
  // Deterministic UTC label: toLocaleTimeString differs between server and
  // browser locale and breaks hydration on the SSR-ed team page.
  const lastLabel = `${last.captured_at.slice(0, 10)} ${last.captured_at.slice(11, 16)} UTC`
  return (
    <svg width="100%" viewBox={`0 0 ${width} ${height}`} className="max-w-2xl" role="img" aria-label="история баллов">
      <line x1={pad} y1={height - pad} x2={width - pad} y2={height - pad} style={{ stroke: 'var(--g-color-line-generic)' }} />
      <line x1={pad} y1={pad} x2={pad} y2={height - pad} style={{ stroke: 'var(--g-color-line-generic)' }} />
      <polyline points={points.join(' ')} fill="none" strokeWidth="2" strokeLinejoin="round" style={{ stroke: 'var(--g-color-text-brand)' }} />
      <text x={pad} y={pad - 8} fontSize="11" style={{ fill: 'var(--g-color-text-hint)' }}>{max.toFixed(1)}</text>
      <text x={pad} y={height - pad + 14} fontSize="11" style={{ fill: 'var(--g-color-text-hint)' }}>{min.toFixed(1)}</text>
      <text x={width - pad} y={height - pad + 14} fontSize="11" textAnchor="end" style={{ fill: 'var(--g-color-text-hint)' }}>
        {lastLabel}
      </text>
    </svg>
  )
}

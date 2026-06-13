import { useState, useEffect, useCallback, useRef } from 'react'
import axios from 'axios'
import {
  AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer,
  CartesianGrid,
} from 'recharts'
import DemoControls from '../components/DemoControls'

const API = import.meta.env.VITE_API_BASE || ''
const REFRESH_INTERVAL = 30_000

/* ─── Helpers ─── */
function formatPrice(amount) {
  if (amount == null) return '—'
  return `₹${Math.round(Number(amount)).toLocaleString('en-IN')}`
}

function formatDateTimeShort(dateStr) {
  const d = new Date(dateStr)
  const day = d.getDate()
  const month = d.toLocaleString('en-IN', { month: 'short' })
  const hours = d.getHours().toString().padStart(2, '0')
  const mins = d.getMinutes().toString().padStart(2, '0')
  return `${day} ${month}, ${hours}:${mins}`
}

function formatDateTimeFull(dateStr) {
  const d = new Date(dateStr)
  const day = d.getDate()
  const month = d.toLocaleString('en-IN', { month: 'short' })
  let hours = d.getHours()
  const mins = d.getMinutes().toString().padStart(2, '0')
  const ampm = hours >= 12 ? 'PM' : 'AM'
  hours = hours % 12 || 12
  return `${day} ${month}, ${hours}:${mins} ${ampm}`
}

/* ─── Toast System ─── */
function ToastContainer({ toasts, removeToast }) {
  return (
    <div className="toast-container">
      {toasts.map((t) => (
        <ToastItem key={t.id} toast={t} onRemove={() => removeToast(t.id)} />
      ))}
    </div>
  )
}

function ToastItem({ toast, onRemove }) {
  const [exiting, setExiting] = useState(false)

  useEffect(() => {
    const exitTimer = setTimeout(() => setExiting(true), toast.duration - 300)
    const removeTimer = setTimeout(onRemove, toast.duration)
    return () => { clearTimeout(exitTimer); clearTimeout(removeTimer) }
  }, [toast.duration, onRemove])

  return (
    <div className={`toast ${exiting ? 'toast-exit' : ''}`}>
      <span style={{ fontSize: '13px', flexShrink: 0, fontWeight: 600 }}>{toast.icon}</span>
      <span style={{ lineHeight: 1.45 }}>{toast.message}</span>
    </div>
  )
}

function useToast() {
  const [toasts, setToasts] = useState([])
  const addToast = useCallback(({ message, type = 'info', icon = 'i', duration = 3000 }) => {
    const id = Date.now() + Math.random()
    setToasts((prev) => [...prev, { id, message, type, icon, duration }])
  }, [])
  const removeToast = useCallback((id) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])
  return { toasts, addToast, removeToast }
}

/* ─── Sparkline ─── */
function Sparkline({ prices }) {
  if (!prices || prices.length < 2) return null

  const last10 = prices.slice(-10)
  const min = Math.min(...last10)
  const max = Math.max(...last10)
  const range = max - min || 1

  const W = 60, H = 24
  const pts = last10.map((p, i) => {
    const x = (i / (last10.length - 1)) * W
    const y = H - ((p - min) / range) * H
    return `${x},${y}`
  })
  const d = `M ${pts.join(' L ')}`

  const trend = last10[last10.length - 1] - last10[0]
  const strokeColor = trend < 0 ? 'var(--accent)' : trend > 0 ? 'var(--red)' : 'var(--text-muted)'

  return (
    <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`} style={{ display: 'block' }}>
      <path d={d} fill="none" stroke={strokeColor} strokeWidth="1.5"
            strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

/* ─── Product Card ─── */
function ProductCard({ product, isSelected, onClick }) {
  const {
    name, platform, current_price, price_change_pct,
    signal, recent_prices,
  } = product

  const platformLabel = platform === 'amazon' ? 'AMAZON' : platform === 'flipkart' ? 'FLIPKART' : platform.toUpperCase()

  return (
    <div
      onClick={onClick}
      className={`product-card ${isSelected ? 'selected' : ''}`}
    >
      {/* Row 1: Platform + Signal */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span className="text-label" style={{ color: 'var(--text-muted)', textTransform: 'uppercase' }}>
          {platformLabel}
        </span>
        {signal === 'BUY' ? (
          <span className="pill pill-buy">BUY</span>
        ) : (
          <span className="pill pill-wait">WAIT</span>
        )}
      </div>

      {/* Row 2: Product Name */}
      <h3 className="text-body" style={{ fontWeight: 500, color: 'var(--text-primary)', marginTop: '12px', display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden', textOverflow: 'ellipsis', height: '2.8em', lineHeight: '1.4em' }}>
        {name}
      </h3>

      {/* Row 3: Price */}
      <div style={{ fontSize: '26px', fontWeight: 600, fontVariantNumeric: 'tabular-nums', letterSpacing: '-0.5px', color: 'var(--text-primary)', marginTop: '8px' }}>
        {formatPrice(current_price)}
      </div>

      {/* Row 4: Change indicator */}
      {(() => {
        if (price_change_pct == null) return <div style={{ fontSize: '12px', color: 'var(--text-muted)', marginTop: '4px' }}>→ No change</div>;
        if (price_change_pct < 0) {
          return (
            <div style={{ fontSize: '12px', color: 'var(--green)', marginTop: '4px' }}>
              ↓ {Math.abs(price_change_pct).toFixed(1)}% today
            </div>
          );
        } else if (price_change_pct > 0) {
          return (
            <div style={{ fontSize: '12px', color: 'var(--red)', marginTop: '4px' }}>
              ↑ {Math.abs(price_change_pct).toFixed(1)}% today
            </div>
          );
        } else {
          return (
            <div style={{ fontSize: '12px', color: 'var(--text-muted)', marginTop: '4px' }}>
              → No change
            </div>
          );
        }
      })()}

      {/* Row 5: Sparkline */}
      {recent_prices && recent_prices.length >= 2 && (
        <div style={{ marginTop: '12px' }}>
          <Sparkline prices={recent_prices} />
        </div>
      )}
    </div>
  )
}

/* ─── Loading Skeleton ─── */
function SkeletonCard() {
  return (
    <div className="product-card skeleton-pulse" style={{ height: '180px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '12px' }}>
        <div style={{ height: '12px', width: '60px', background: 'var(--border-strong)', borderRadius: '4px' }} />
        <div style={{ height: '12px', width: '40px', background: 'var(--border-strong)', borderRadius: '4px' }} />
      </div>
      <div style={{ height: '14px', width: '80%', background: 'var(--border-strong)', borderRadius: '4px', marginBottom: '8px' }} />
      <div style={{ height: '14px', width: '50%', background: 'var(--border-strong)', borderRadius: '4px', marginBottom: '24px' }} />
      <div style={{ height: '28px', width: '120px', background: 'var(--border-strong)', borderRadius: '4px' }} />
    </div>
  )
}

/* ─── Chart Skeleton ─── */
function ChartSkeleton() {
  return (
    <div className="skeleton-pulse" style={{ height: '300px', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--bg-surface)', border: '1px solid var(--border-subtle)', borderRadius: '8px' }}>
      <span className="text-label" style={{ color: 'var(--text-secondary)' }}>Loading history...</span>
    </div>
  )
}

/* ─── Empty State ─── */
function ChartEmpty() {
  return (
    <div style={{ height: '300px', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: '1rem', background: 'var(--bg-surface)', border: '1px solid var(--border-subtle)', borderRadius: '8px' }}>
      <div style={{ textAlign: 'center' }}>
        <p className="text-body" style={{ fontWeight: 500, color: 'var(--text-secondary)', marginBottom: '4px' }}>
          Price history building...
        </p>
        <p className="text-label" style={{ color: 'var(--text-muted)' }}>
          Check back in 30 minutes
        </p>
      </div>
    </div>
  )
}

/* ─── Custom Tooltip ─── */
function ChartTooltip({ active, payload, label, allData }) {
  if (!active || !payload?.length) return null

  const current = payload[0].value
  const currentIndex = allData?.findIndex(d => d.date === label) ?? -1
  const prev = currentIndex > 0 ? allData[currentIndex - 1]?.price : null
  const delta = prev != null ? current - prev : null
  const deltaAbs = delta != null ? Math.abs(Math.round(delta)) : null
  const deltaPct = prev != null && prev > 0 ? Math.abs(delta / prev * 100).toFixed(1) : null
  const deltaUp = delta > 0

  return (
    <div style={{
      background: 'var(--bg-overlay)',
      border: '1px solid var(--border-default)',
      borderRadius: '6px',
      padding: '12px 16px',
      minWidth: '160px',
    }}>
      <p className="text-label" style={{ color: 'var(--text-muted)', marginBottom: '6px' }}>
        {formatDateTimeFull(label)}
      </p>
      <p className="text-body" style={{ fontWeight: 600, color: 'var(--text-primary)', marginBottom: delta != null ? '6px' : 0 }}>
        {formatPrice(current)}
      </p>
      {delta != null && deltaAbs != null && (
        <p className="text-mono" style={{ fontSize: '11px', fontWeight: 500, color: deltaUp ? 'var(--red)' : 'var(--green)' }}>
          {deltaUp ? '↑' : '↓'} ₹{deltaAbs.toLocaleString('en-IN')}
          {deltaPct && ` (${deltaPct}%)`}
        </p>
      )}
    </div>
  )
}

/* ─── Price History Chart Panel ─── */
function PriceChartPanel({ product }) {
  const [range, setRange] = useState('7d')
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  const productId = product?.id

  useEffect(() => {
    if (!productId) return
    let cancelled = false
    setLoading(true)
    setError(null)
    setData(null)

    axios
      .get(`${API}/api/products/${productId}/history`, { params: { range } })
      .then((res) => {
        if (!cancelled) { setData(res.data); setLoading(false) }
      })
      .catch((err) => {
        if (!cancelled) { setError(err.message); setLoading(false) }
      })

    return () => { cancelled = true }
  }, [productId, range])

  if (!product) return null

  const rawHistory = data?.history || []
  const chartData = rawHistory.map((h) => ({
    date: h.scraped_at,
    price: Math.round(h.price),
    in_stock: h.in_stock,
  }))

  const stats = data?.stats
  const hasEnoughData = chartData.length >= 2

  const getTickIndices = (len, maxTicks = 6) => {
    if (len <= maxTicks) return chartData.map((d) => d.date)
    const step = Math.floor(len / (maxTicks - 1))
    const indices = []
    for (let i = 0; i < maxTicks - 1; i++) indices.push(i * step)
    indices.push(len - 1)
    return indices.map((i) => chartData[i].date)
  }
  const ticks = hasEnoughData ? getTickIndices(chartData.length) : []

  return (
    <div className="chart-panel-container">
      {/* Header row */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '24px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <h2 className="text-heading">{product.name}</h2>
          {product.price_source === 'live' && (
            <span className="pill pill-live">LIVE</span>
          )}
          {product.price_source === 'estimated' && (
            <span className="pill pill-estimated">ESTIMATED</span>
          )}
          {product.price_source === 'simulated' && (
            <span className="pill pill-simulated">SIMULATED</span>
          )}
        </div>

        {/* Range toggle */}
        <div style={{ display: 'flex', gap: '4px' }}>
          {['7d', '30d', 'all'].map((r) => (
            <button
              key={r}
              onClick={() => setRange(r)}
              className={range === r ? 'range-btn-active' : 'range-btn-inactive'}
            >
              {r.toUpperCase()}
            </button>
          ))}
        </div>
      </div>

      {/* Stats row */}
      {stats && !loading && (
        <div className="stats-grid">
          <div className="stat-item">
            <span className="text-subhead" style={{ fontSize: '11px' }}>CURRENT</span>
            <span style={{ fontSize: '18px', fontWeight: 600, fontVariantNumeric: 'tabular-nums', color: 'var(--text-primary)' }}>
              {formatPrice(product.current_price)}
            </span>
          </div>
          <div className="stat-item">
            <span className="text-subhead" style={{ fontSize: '11px' }}>30D HIGH</span>
            <span style={{ fontSize: '18px', fontWeight: 600, fontVariantNumeric: 'tabular-nums', color: 'var(--red)' }}>
              {formatPrice(stats.max_price)}
            </span>
          </div>
          <div className="stat-item">
            <span className="text-subhead" style={{ fontSize: '11px' }}>30D LOW</span>
            <span style={{ fontSize: '18px', fontWeight: 600, fontVariantNumeric: 'tabular-nums', color: 'var(--green)' }}>
              {formatPrice(stats.min_price)}
            </span>
          </div>
          <div className="stat-item">
            <span className="text-subhead" style={{ fontSize: '11px' }}>AVG</span>
            <span style={{ fontSize: '18px', fontWeight: 600, fontVariantNumeric: 'tabular-nums', color: 'var(--text-primary)' }}>
              {formatPrice(stats.avg_price)}
            </span>
          </div>
        </div>
      )}

      {/* Chart */}
      <div>
        {loading ? (
          <ChartSkeleton />
        ) : error ? (
          <div style={{ height: '300px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <p className="text-body" style={{ color: 'var(--red)' }}>Failed to load chart: {error}</p>
          </div>
        ) : !hasEnoughData ? (
          <ChartEmpty />
        ) : (
          <div style={{ height: '300px' }}>
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={chartData} margin={{ top: 8, right: 8, bottom: 0, left: 8 }}>
                <defs>
                  <linearGradient id="blueGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="var(--accent)" stopOpacity={0.15} />
                    <stop offset="100%" stopColor="var(--accent)" stopOpacity={0} />
                  </linearGradient>
                </defs>

                <CartesianGrid strokeDasharray="1 4" stroke="var(--border-subtle)" vertical={false} />

                <XAxis
                  dataKey="date"
                  tickFormatter={formatDateTimeShort}
                  ticks={ticks}
                  tick={{ fill: 'var(--text-muted)', fontSize: 11 }}
                  axisLine={false}
                  tickLine={false}
                  dy={8}
                />

                <YAxis
                  tickFormatter={(v) => `₹${Math.round(v).toLocaleString('en-IN')}`}
                  tick={{ fill: 'var(--text-muted)', fontSize: 11 }}
                  axisLine={false}
                  tickLine={false}
                  dx={-4}
                  width={80}
                />

                <Tooltip
                  content={<ChartTooltip allData={chartData} />}
                  cursor={{ stroke: 'rgba(59,130,246,0.2)', strokeWidth: 1, strokeDasharray: '1 4' }}
                />

                <Area
                  type="monotone"
                  dataKey="price"
                  stroke="var(--accent)"
                  strokeWidth={1.5}
                  fill="url(#blueGradient)"
                  animationDuration={200}
                  animationEasing="ease-in-out"
                  dot={false}
                  activeDot={{ r: 4, stroke: 'var(--accent)', strokeWidth: 1.5, fill: 'var(--bg-surface)' }}
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        )}
      </div>
    </div>
  )
}

/* ═══════════════════════════════════════════
   ███  DASHBOARD PAGE
   ═══════════════════════════════════════════ */
export default function Dashboard() {
  const [products, setProducts] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [selectedProduct, setSelectedProduct] = useState(null)
  const [newProductId, setNewProductId] = useState(null)

  // Add product form
  const [url, setUrl] = useState('')
  const [addLoading, setAddLoading] = useState(false)

  const [healthOk, setHealthOk] = useState(null)

  const refreshTimerRef = useRef(null)
  const { toasts, addToast, removeToast } = useToast()

  const fetchProducts = useCallback(async () => {
    try {
      const res = await axios.get(`${API}/api/products`)
      const raw = res.data.products || []
      setProducts(raw)
      setError(null)
      return raw
    } catch (err) {
      setError(err.message)
      return []
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    const checkHealth = () => {
      axios
        .get(`${API}/health`)
        .then(() => setHealthOk(true))
        .catch(() => setHealthOk(false))
    }
    checkHealth()
    const healthTimer = setInterval(checkHealth, 10_000)
    return () => clearInterval(healthTimer)
  }, [])

  useEffect(() => {
    const init = async () => {
      const raw = await fetchProducts()
      const visible = raw.filter(
        (p) => p.current_price > 0 && p.name && p.name !== 'Unnamed Product' && p.name.trim() !== ''
      )
      if (visible.length > 0) {
        setSelectedProduct((prev) => prev ?? visible[0])
      }
    }
    init()
    refreshTimerRef.current = setInterval(fetchProducts, REFRESH_INTERVAL)
    return () => clearInterval(refreshTimerRef.current)
  }, [fetchProducts])

  useEffect(() => {
    if (!newProductId) return
    const t = setTimeout(() => setNewProductId(null), 2500)
    return () => clearTimeout(t)
  }, [newProductId])

  const visibleProducts = products.filter(
    (p) => p.current_price > 0 && p.name && p.name !== 'Unnamed Product' && p.name.trim() !== ''
  )

  const handleAddProduct = async (e) => {
    e.preventDefault()
    if (!url.trim()) return
    setAddLoading(true)
    try {
      const res = await axios.post(`${API}/api/products`, { url: url.trim() })
      const data = res.data
      setUrl('')
      const fresh = await fetchProducts()

      const pData = data.product || {}
      const name = pData.name || 'Product'
      const source = data.price_source

      if (source === 'live') {
        const price = data.fetched_price ? formatPrice(data.fetched_price) : ''
        addToast({
          message: `Tracking ${name}${price ? ` — current price ${price} (live)` : ''}`,
          type: 'success',
          icon: '✓',
        })
      } else if (source === 'estimated') {
        const price = data.fetched_price ? formatPrice(data.fetched_price) : ''
        addToast({
          message: `Tracking ${name}${price ? ` — current price ${price} (estimated)` : ''}`,
          type: 'success',
          icon: '✓',
        })
      } else {
        addToast({
          message: `Tracking ${name} — price simulated (using category guess)`,
          type: 'info',
          icon: '»',
        })
      }

      if (pData.id) {
        setNewProductId(pData.id)
        const newProd = fresh.find((p) => p.id === pData.id)
        if (newProd) setSelectedProduct(newProd)
      }

    } catch (err) {
      const msg = err.response?.data?.error || err.message
      addToast({ message: `Failed to add product: ${msg}`, type: 'error', icon: '!' })
    } finally {
      setAddLoading(false)
    }
  }

  const handleCardClick = (product) => {
    setSelectedProduct((prev) => prev?.id === product.id ? null : product)
  }

  return (
    <div className="app">
      <ToastContainer toasts={toasts} removeToast={removeToast} />

      {/* Sidebar */}
      <aside className="sidebar">
        <div style={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
          {/* Logo */}
          <div className="text-heading" style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '4px 8px' }}>
            <span style={{ color: 'var(--accent)' }}>◈</span>
            PriceSpy
          </div>
          
          {/* Nav items */}
          <nav style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
            <div className="text-body" style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '8px 12px', background: 'var(--bg-elevated)', borderRadius: '6px', cursor: 'pointer', fontWeight: 500 }}>
              <span style={{ color: 'var(--accent)' }}>◈</span> Dashboard
            </div>
            <div className="text-body" style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '8px 12px', color: 'var(--text-secondary)', cursor: 'pointer' }}>
              <span style={{ color: 'var(--text-muted)' }}>◈</span> Alerts
            </div>
            <div className="text-body" style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '8px 12px', color: 'var(--text-secondary)', cursor: 'pointer' }}>
              <span style={{ color: 'var(--text-muted)' }}>◈</span> History
            </div>
            <div className="text-body" style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '8px 12px', color: 'var(--text-secondary)', cursor: 'pointer' }}>
              <span style={{ color: 'var(--text-muted)' }}>◈</span> Settings
            </div>
          </nav>
        </div>

        <div>
          {/* Demo Controls */}
          <DemoControls onRefresh={fetchProducts} onToast={addToast} />
          
          {/* Status Indicator */}
          <div style={{ marginTop: '16px', paddingTop: '16px', borderTop: '1px solid var(--border-subtle)', display: 'flex', alignItems: 'center', gap: '6px', fontSize: '11px', fontWeight: 500 }}>
            {healthOk === true ? (
              <>
                <span style={{ color: 'var(--green)' }}>●</span>
                <span style={{ color: 'var(--text-secondary)' }}>Live</span>
              </>
            ) : (
              <>
                <span style={{ color: 'var(--red)' }}>●</span>
                <span style={{ color: 'var(--text-secondary)' }}>Offline</span>
              </>
            )}
          </div>
        </div>
      </aside>

      {/* Main Content Area */}
      <main className="main">
        {/* Topbar */}
        <header className="topbar">
          <div className="text-subhead">Dashboard</div>
          <form onSubmit={handleAddProduct} style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
            <input
              type="url"
              id="product-url-input"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="amazon.in/dp/... or flipkart.com/p/..."
              className="input-text"
              required
            />
            <button
              type="submit"
              id="track-price-btn"
              disabled={addLoading || !url.trim()}
              className="btn-track"
            >
              {addLoading ? '...' : 'Track'}
            </button>
          </form>
        </header>

        {/* Content */}
        <div className="content">
          {loading && products.length === 0 ? (
            <div className="product-grid-container">
              {Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)}
            </div>
          ) : error && products.length === 0 ? (
            <div className="chart-panel-container" style={{ padding: '2.5rem', textAlign: 'center' }}>
              <p className="text-body" style={{ color: 'var(--red)', marginBottom: '1rem' }}>Failed to load products</p>
              <p className="text-mono" style={{ fontSize: '12px', color: 'var(--text-secondary)' }}>{error}</p>
              <button onClick={() => { setLoading(true); fetchProducts() }}
                      className="btn-track" style={{ marginTop: '1rem', fontSize: '12px' }}>
                Retry
              </button>
            </div>
          ) : visibleProducts.length === 0 ? (
            <div className="chart-panel-container" style={{ padding: '2.5rem', textAlign: 'center' }}>
              <p className="text-heading" style={{ color: 'var(--text-secondary)', marginBottom: '8px' }}>No products tracked yet</p>
              <p className="text-body" style={{ color: 'var(--text-muted)' }}>
                Paste an Amazon or Flipkart URL above to get started.
              </p>
            </div>
          ) : (
            <div className="product-grid-container">
              {visibleProducts.map((product) => (
                <ProductCard
                  key={product.id}
                  product={product}
                  isSelected={selectedProduct?.id === product.id}
                  onClick={() => handleCardClick(product)}
                />
              ))}
            </div>
          )}

          {/* Selected Product Chart */}
          {selectedProduct && (
            <PriceChartPanel
              product={selectedProduct}
            />
          )}
        </div>
      </main>
    </div>
  )
}

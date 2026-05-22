package httpapi

import (
	"net/http"
)

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Apprentice Dashboard</title>
<script src="https://cdn.jsdelivr.net/npm/vue@3/dist/vue.global.prod.js"></script>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4/dist/chart.umd.min.js"></script>
<style>
:root {
  --charcoal: #2e4057;
  --pollen: #ffd151;
  --tuscan: #f8c537;
  --sunflower: #edb230;
  --pumpkin: #e77728;
  --bg: #1a2636;
  --card: #354a61;
  --text: #e8e8e8;
  --text-dim: #9ba8b8;
}
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  background: var(--bg);
  color: var(--text);
  min-height: 100vh;
}
header {
  background: var(--charcoal);
  padding: 1rem 2rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 2px solid var(--pollen);
}
header h1 {
  font-size: 1.5rem;
  color: var(--pollen);
  letter-spacing: 0.05em;
}
header .status {
  font-size: 0.8rem;
  color: var(--text-dim);
}
header .status .dot {
  display: inline-block;
  width: 8px; height: 8px;
  border-radius: 50%;
  background: #4caf50;
  margin-right: 4px;
}
.container { max-width: 1200px; margin: 0 auto; padding: 1.5rem; }
.tabs {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 1.5rem;
}
.tabs button {
  background: var(--card);
  color: var(--text-dim);
  border: 1px solid transparent;
  padding: 0.5rem 1.2rem;
  border-radius: 6px;
  cursor: pointer;
  font-size: 0.9rem;
  transition: all 0.2s;
}
.tabs button:hover { border-color: var(--sunflower); color: var(--text); }
.tabs button.active {
  background: var(--charcoal);
  color: var(--pollen);
  border-color: var(--pollen);
}
.card {
  background: var(--card);
  border-radius: 10px;
  padding: 1.5rem;
  margin-bottom: 1.5rem;
  border: 1px solid rgba(255,209,81,0.1);
}
.card h2 {
  font-size: 1.1rem;
  color: var(--tuscan);
  margin-bottom: 1rem;
  padding-bottom: 0.5rem;
  border-bottom: 1px solid rgba(255,209,81,0.15);
}
table { width: 100%; border-collapse: collapse; }
th, td {
  padding: 0.6rem 0.8rem;
  text-align: left;
  border-bottom: 1px solid rgba(255,255,255,0.05);
}
th {
  color: var(--sunflower);
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
td { font-size: 0.9rem; }
.badge {
  display: inline-block;
  padding: 0.2rem 0.6rem;
  border-radius: 12px;
  font-size: 0.75rem;
  font-weight: 600;
}
.badge.positive { background: rgba(76,175,80,0.2); color: #81c784; }
.badge.negative { background: rgba(231,119,40,0.2); color: var(--pumpkin); }
.chart-container { position: relative; height: 300px; }
.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 1rem;
}
.stat-card {
  background: var(--charcoal);
  border-radius: 8px;
  padding: 1rem;
  text-align: center;
}
.stat-card .label {
  font-size: 0.75rem;
  color: var(--text-dim);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.stat-card .value {
  font-size: 1.8rem;
  font-weight: 700;
  color: var(--pollen);
  margin-top: 0.3rem;
}
.stat-card .sub {
  font-size: 0.8rem;
  color: var(--text-dim);
  margin-top: 0.2rem;
}
.loading {
  text-align: center;
  padding: 3rem;
  color: var(--text-dim);
}
select {
  background: var(--charcoal);
  color: var(--text);
  border: 1px solid var(--sunflower);
  padding: 0.4rem 0.8rem;
  border-radius: 4px;
  font-size: 0.85rem;
}
</style>
</head>
<body>
<div id="app">
  <header>
    <h1>Apprentice Dashboard</h1>
    <div class="status">
      <span class="dot"></span>
      <span>Live &middot; Last refresh {{ lastRefresh }}</span>
    </div>
  </header>
  <div class="container">
    <div class="tabs">
      <button :class="{ active: tab === 'roi' }" @click="tab = 'roi'; fetchAll()">ROI</button>
      <button :class="{ active: tab === 'usage' }" @click="tab = 'usage'; fetchUsage()">Usage</button>
      <button :class="{ active: tab === 'latency' }" @click="tab = 'latency'; fetchLatency()">Latency</button>
      <button :class="{ active: tab === 'pods' }" @click="tab = 'pods'; fetchRunPod()">Pods</button>
    </div>

    <div v-if="tab === 'roi'">
      <div class="card">
        <h2>ROI Summary</h2>
        <div v-if="roi.length === 0" class="loading">No training runs recorded yet.</div>
        <table v-else>
          <thead>
            <tr>
              <th>Pattern</th>
              <th>Train Cost</th>
              <th>Saved</th>
              <th>ROI</th>
              <th>Status</th>
              <th>Runs</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in roi" :key="r.pattern_id">
              <td style="color: var(--pollen); font-weight: 600;">{{ r.pattern_id }}</td>
              <td>$ {{ r.train_cost.toFixed(4) }}</td>
              <td>$ {{ r.saved.toFixed(4) }}</td>
              <td>$ {{ r.roi.toFixed(4) }}</td>
              <td>
                <span class="badge" :class="r.broke_even ? 'positive' : 'negative'">
                  {{ r.broke_even ? 'Broke Even' : 'In Progress' }}
                </span>
              </td>
              <td>{{ r.runs }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-if="tab === 'usage'">
      <div class="card">
        <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 1rem;">
          <h2 style="margin: 0; border: none; padding: 0;">Usage Over Time</h2>
          <div style="display: flex; gap: 0.5rem; align-items: center;">
            <label style="font-size: 0.8rem; color: var(--text-dim);">Pattern:</label>
            <select v-model="usagePattern" @change="fetchUsage()">
              <option value="">All Patterns</option>
              <option v-for="r in roi" :key="r.pattern_id" :value="r.pattern_id">{{ r.pattern_id }}</option>
            </select>
            <label style="font-size: 0.8rem; color: var(--text-dim);">Bucket:</label>
            <select v-model="usageBucket" @change="fetchUsage()">
              <option value="day">Day</option>
              <option value="hour">Hour</option>
              <option value="week">Week</option>
            </select>
          </div>
        </div>
        <div class="chart-container">
          <canvas ref="usageChart"></canvas>
        </div>
      </div>
    </div>

    <div v-if="tab === 'latency'">
      <div class="card">
        <h2>Latency Statistics</h2>
        <div v-if="!latency" class="loading">Loading...</div>
        <div v-else class="stats-grid">
          <div class="stat-card">
            <div class="label">Specialist Avg</div>
            <div class="value">{{ latency.specialist.avg }}<span style="font-size: 0.9rem;"> ms</span></div>
            <div class="sub">{{ latency.specialist.count }} requests</div>
          </div>
          <div class="stat-card">
            <div class="label">Specialist P95</div>
            <div class="value" style="color: var(--tuscan);">{{ latency.specialist.p95 }}<span style="font-size: 0.9rem;"> ms</span></div>
          </div>
          <div class="stat-card">
            <div class="label">Upstream Avg</div>
            <div class="value" style="color: var(--pumpkin);">{{ latency.upstream.avg }}<span style="font-size: 0.9rem;"> ms</span></div>
            <div class="sub">{{ latency.upstream.count }} requests</div>
          </div>
          <div class="stat-card">
            <div class="label">Upstream P95</div>
            <div class="value" style="color: var(--pumpkin);">{{ latency.upstream.p95 }}<span style="font-size: 0.9rem;"> ms</span></div>
          </div>
        </div>
      </div>
    </div>

    <div v-if="tab === 'pods'">
      <div class="card">
        <h2>RunPod Pods</h2>
        <div v-if="runpodError" style="text-align: center; padding: 2rem;">
          <p style="color: var(--pumpkin); margin-bottom: 1rem;">{{ runpodError }}</p>
          <p style="color: var(--text-dim); font-size: 0.85rem;">
            Configure with <code style="background: var(--charcoal); padding: 0.2rem 0.5rem; border-radius: 4px; color: var(--pollen);">--runpod-api-key</code>
          </p>
        </div>
        <div v-else-if="!runpod" class="loading">Loading pod data...</div>
        <div v-else>
          <div class="stats-grid" style="margin-bottom: 1rem;">
            <div class="stat-card">
              <div class="label">Active Pods</div>
              <div class="value">{{ runpod.pods ? runpod.pods.length : 0 }}</div>
            </div>
            <div class="stat-card">
              <div class="label">Total Cost / Hr</div>
              <div class="value" style="color: var(--tuscan);">$ {{ runpod.total_cost_hr.toFixed(4) }}</div>
            </div>
            <div class="stat-card">
              <div class="label">Total Accrued</div>
              <div class="value" style="color: var(--pumpkin);">$ {{ runpod.total_accrued.toFixed(4) }}</div>
            </div>
          </div>
          <table v-if="runpod.pods && runpod.pods.length > 0">
            <thead>
              <tr>
                <th>Name</th>
                <th>Status</th>
                <th>$/Hr</th>
                <th>Uptime</th>
                <th>Accrued</th>
                <th>GPU</th>
                <th>Mem</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="p in runpod.pods" :key="p.id">
                <td style="color: var(--pollen);">{{ p.name }}</td>
                <td>
                  <span class="badge" :class="p.status === 'RUNNING' ? 'positive' : 'negative'">
                    {{ p.status }}
                  </span>
                </td>
                <td>$ {{ p.cost_per_hr.toFixed(4) }}</td>
                <td>{{ p.uptime_hours.toFixed(1) }}h</td>
                <td>$ {{ p.accrued_cost.toFixed(4) }}</td>
                <td>{{ p.gpu_util_pct.toFixed(0) }}%</td>
                <td>{{ p.memory_util_pct.toFixed(0) }}%</td>
              </tr>
            </tbody>
          </table>
          <p v-else style="text-align: center; color: var(--text-dim); padding: 2rem;">No active pods.</p>
        </div>
      </div>
    </div>
  </div>
</div>

<script>
const { createApp, ref, onMounted, nextTick } = Vue;

createApp({
  setup() {
    const tab = ref('roi');
    const roi = ref([]);
    const usage = ref([]);
    const latency = ref(null);
    const runpod = ref(null);
    const runpodError = ref('');
    const lastRefresh = ref('--:--:--');
    const usagePattern = ref('');
    const usageBucket = ref('day');
    const usageChart = ref(null);
    let chartInstance = null;

    const fmtTime = () => new Date().toLocaleTimeString();

    async function fetchAll() {
      try {
        const res = await fetch('/api/cost/roi');
        const data = await res.json();
        roi.value = Array.isArray(data) ? data : [];
      } catch (e) {
        roi.value = [];
      }
      lastRefresh.value = fmtTime();
    }

    async function fetchUsage() {
      try {
        const params = new URLSearchParams();
        if (usagePattern.value) params.set('pattern_id', usagePattern.value);
        params.set('bucket', usageBucket.value);
        const res = await fetch('/api/cost/usage?' + params.toString());
        const data = await res.json();
        usage.value = Array.isArray(data) ? data : [];
      } catch (e) {
        usage.value = [];
      }
      await nextTick();
      renderChart();
      lastRefresh.value = fmtTime();
    }

    async function fetchLatency() {
      try {
        const res = await fetch('/api/cost/latency');
        const data = await res.json();
        latency.value = data;
      } catch (e) {
        latency.value = null;
      }
      lastRefresh.value = fmtTime();
    }

    async function fetchRunPod() {
      try {
        const res = await fetch('/api/cost/runpod');
        if (res.status === 503) {
          runpodError.value = 'RunPod API key is not configured.';
          runpod.value = null;
        } else if (!res.ok) {
          runpodError.value = 'RunPod API error (HTTP ' + res.status + ').';
          runpod.value = null;
        } else {
          const data = await res.json();
          runpod.value = data;
          runpodError.value = '';
        }
      } catch (e) {
        runpodError.value = 'Failed to reach RunPod API.';
        runpod.value = null;
      }
      lastRefresh.value = fmtTime();
    }

    function renderChart() {
      if (!usageChart.value) return;
      if (chartInstance) chartInstance.destroy();
      const ctx = usageChart.value.getContext('2d');
      chartInstance = new Chart(ctx, {
        type: 'line',
        data: {
          labels: usage.value.map(b => b.time),
          datasets: [
            {
              label: 'Requests',
              data: usage.value.map(b => b.requests),
              borderColor: '#ffd151',
              backgroundColor: 'rgba(255,209,81,0.1)',
              fill: true,
              tension: 0.3,
              yAxisID: 'y',
            },
            {
              label: 'Cost Saved ($)',
              data: usage.value.map(b => b.cost_saved),
              borderColor: '#e77728',
              backgroundColor: 'rgba(231,119,40,0.1)',
              fill: true,
              tension: 0.3,
              yAxisID: 'y1',
            }
          ]
        },
        options: {
          responsive: true,
          maintainAspectRatio: false,
          plugins: {
            legend: { labels: { color: '#9ba8b8' } }
          },
          scales: {
            x: {
              ticks: { color: '#9ba8b8' },
              grid: { color: 'rgba(255,255,255,0.05)' }
            },
            y: {
              position: 'left',
              ticks: { color: '#ffd151' },
              grid: { color: 'rgba(255,255,255,0.05)' },
              title: { display: true, text: 'Requests', color: '#ffd151' }
            },
            y1: {
              position: 'right',
              ticks: { color: '#e77728' },
              grid: { drawOnChartArea: false },
              title: { display: true, text: 'Cost Saved ($)', color: '#e77728' }
            }
          }
        }
      });
    }

    onMounted(() => { fetchAll(); });

    return {
      tab, roi, usage, latency, runpod, runpodError, lastRefresh,
      usagePattern, usageBucket, usageChart,
      fetchAll, fetchUsage, fetchLatency, fetchRunPod,
    };
  }
}).mount('#app');
</script>
</body>
</html>`

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(dashboardHTML))
}

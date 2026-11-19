// Repo page: interactive chart (inline SVG + hover tooltip), chart options
// builder and status polling while the repo is being analyzed.
(function () {
  const el = document.getElementById('app');
  if (!el) return;
  const cfg = JSON.parse(el.dataset.config);

  const current = Object.fromEntries(new URLSearchParams(cfg.query));
  const base = { top: '5', x: 'commits', mode: 'stacked', metric: 'code', theme: 'auto', exclude: '' };

  const metricOf = {
    code: (s) => s.code || 0,
    lines: (s) => (s.code || 0) + (s.comments || 0) + (s.blanks || 0),
    comments: (s) => s.comments || 0,
    files: (s) => s.files || 0,
  };

  // index of the last point with time <= t (points are sorted by monotonic time)
  function searchTime(points, t) {
    let lo = 0, hi = points.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (points[mid].time <= t) lo = mid; else hi = mid - 1;
    }
    return lo;
  }

  Vue.createApp({
    delimiters: ['${', '}'],
    data() {
      return {
        status: cfg.status,
        hasChart: cfg.hasChart,
        progress: null,
        opts: Object.assign({}, base, current),
        version: 0,
        copied: false,
        svg: '',       // inline interactive SVG markup
        layout: null,  // geometry parsed from its data-layout attribute
        points: null,  // full series with commit metadata
        hit: null,     // hovered commit
        tipStyle: {},
      };
    },
    computed: {
      // options as they appear in embed URLs (only non defaults)
      query() {
        const q = new URLSearchParams();
        for (const [k, v] of Object.entries(this.opts)) {
          if (String(v).trim() !== '' && String(v) !== String(base[k])) q.set(k, String(v).trim());
        }
        return q.toString();
      },
      embedURL() {
        return cfg.pageURL + (this.query ? '?' + this.query : '');
      },
      imageSrc() {
        const q = new URLSearchParams(this.query);
        if (this.version) q.set('v', this.version);
        const s = q.toString();
        return '/' + cfg.slug + '.svg' + (s ? '?' + s : '');
      },
      interactiveSrc() {
        const q = new URLSearchParams(this.query);
        q.set('interactive', '1');
        return '/' + cfg.slug + '.svg?' + q.toString();
      },
      markdown() {
        return '[![Lines of code](' + this.embedURL + ')](' + cfg.pageURL + ')';
      },
      percentMode() {
        return this.layout && this.layout.mode === 'percent';
      },
      progressText() {
        const p = this.progress;
        if (!p) return this.status === 'pending' ? 'Waiting in the queue…' : 'Working…';
        if (p.phase === 'sync') return 'Cloning repository…';
        if (p.total) return 'Counting lines: commit ' + p.done + ' of ' + p.total;
        return 'Counting lines…';
      },
    },
    watch: {
      query(q) {
        history.replaceState(null, '', '/' + cfg.slug + (q ? '?' + q : ''));
        this.loadSVG();
      },
    },
    mounted() {
      if (this.status === 'pending' || this.status === 'processing') {
        this.poll();
        return;
      }
      if (this.hasChart) {
        this.loadSVG();
        this.loadPoints();
      }
    },
    methods: {
      async loadSVG() {
        if (!this.hasChart) return;
        const src = this.interactiveSrc;
        try {
          const r = await fetch(src);
          const text = await r.text();
          if (src !== this.interactiveSrc) return; // options changed meanwhile
          const doc = new DOMParser().parseFromString(text, 'image/svg+xml');
          const root = doc.documentElement;
          const raw = root && root.getAttribute('data-layout');
          this.layout = raw ? JSON.parse(raw) : null;
          this.svg = text;
          this.hit = null;
        } catch (e) {
          console.warn('svg', e);
        }
      },
      async loadPoints() {
        try {
          const r = await fetch('/' + cfg.slug + '.json');
          const body = await r.json();
          this.points = body.points || [];
        } catch (e) {
          console.warn('points', e);
        }
      },

      // --- hover -------------------------------------------------------
      hover(ev) {
        const L = this.layout, P = this.points;
        if (!L || !P || !P.length || !this.$refs.box) return;
        const svgEl = this.$refs.box.querySelector('svg');
        if (!svgEl) return;

        const rect = svgEl.getBoundingClientRect();
        const boxRect = this.$refs.box.getBoundingClientRect();
        const k = rect.width / L.width; // css px per svg unit
        const [pl, pt, pr, pb] = L.plot;
        const sx = (ev.clientX - rect.left) / k; // svg units
        const sy = (ev.clientY - rect.top) / k;
        if (sx < pl - 12 || sx > pr + 12 || sy < pt - 12 || sy > pb + 12) {
          this.hit = null;
          return;
        }
        const f = Math.min(1, Math.max(0, (sx - pl) / (pr - pl)));
        const [d0, d1] = L.domain;

        let i, px;
        if (d1 <= d0) {
          i = P.length - 1; // single point or all commits at the same time
          px = sx;
        } else if (L.x === 'commits') {
          i = Math.round(d0 + f * (d1 - d0));
          px = pl + ((i - d0) / (d1 - d0)) * (pr - pl);
        } else {
          const t = d0 + f * (d1 - d0);
          i = searchTime(P, t);
          if (i + 1 < P.length && Math.abs(P[i + 1].time - t) < Math.abs(t - P[i].time)) i++;
          px = pl + ((P[i].time - d0) / (d1 - d0)) * (pr - pl);
        }
        i = Math.min(P.length - 1, Math.max(0, i));
        if (this.hit && this.hit.index === i && Math.abs(this.hit.x - (rect.left - boxRect.left + px * k)) < 0.5) {
          this.placeTip(ev, boxRect);
          return;
        }

        const metric = metricOf[L.metric] || metricOf.code;
        const groupValue = (point, g) => {
          let v = 0;
          if (!point) return 0;
          for (const lang of g.langs) {
            const s = point.languages[lang];
            if (s) v += metric(s);
          }
          return v;
        };
        const point = P[i], prev = i > 0 ? P[i - 1] : null;
        const values = L.groups.map((g) => groupValue(point, g));
        const total = values.reduce((a, b) => a + b, 0);
        const prevTotal = prev ? L.groups.reduce((a, g) => a + groupValue(prev, g), 0) : 0;

        // dots on the top edge of each band (or on each line)
        const ox = rect.left - boxRect.left, oy = rect.top - boxRect.top;
        const yOf = (v) => oy + (pb - (v / L.ymax) * (pb - pt)) * k;
        const dots = [];
        let acc = 0;
        L.groups.forEach((g, gi) => {
          let v = values[gi];
          if (L.mode === 'percent') v = total > 0 ? (v * 100) / total : 0;
          if (L.mode === 'lines') {
            dots.push({ y: yOf(v), color: g.color });
          } else {
            acc += v;
            if (values[gi] > 0) dots.push({ y: yOf(acc), color: g.color });
          }
        });

        this.hit = {
          index: i,
          point,
          x: ox + px * k,
          top: oy + pt * k,
          height: (pb - pt) * k,
          dots,
          total,
          totalDelta: prev ? total - prevTotal : total,
          rows: L.groups
            .map((g, gi) => ({
              name: g.name,
              color: g.color,
              value: values[gi],
              percent: total > 0 ? (values[gi] * 100) / total : 0,
              delta: prev ? values[gi] - groupValue(prev, g) : values[gi],
            }))
            .filter((r) => r.value > 0 || r.delta !== 0),
        };
        this.$nextTick(() => this.placeTip(ev, boxRect));
      },
      placeTip(ev, boxRect) {
        const tip = this.$refs.tip;
        if (!tip || !this.hit) return;
        const w = tip.offsetWidth, h = tip.offsetHeight;
        const gap = 14;
        let left = this.hit.x + gap;
        if (left + w > boxRect.width - 4) left = this.hit.x - gap - w;
        left = Math.max(4, left);
        let top = ev.clientY - boxRect.top - h / 2;
        top = Math.max(4, Math.min(boxRect.height - h - 4, top));
        this.tipStyle = { left: left + 'px', top: top + 'px' };
      },
      unhover() {
        this.hit = null;
      },
      openCommit() {
        if (!this.hit) return;
        window.open('https://' + cfg.slug + '/commit/' + this.hit.point.commit, '_blank', 'noopener');
      },

      // --- formatting --------------------------------------------------
      formatDate(p) {
        const d = new Date((p.date || p.time) * 1000);
        return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
      },
      formatValue(v) {
        return Math.round(v).toLocaleString();
      },
      formatDelta(d) {
        if (!d) return '';
        return (d > 0 ? '+' : '−') + Math.abs(Math.round(d)).toLocaleString();
      },

      // --- status / misc -----------------------------------------------
      async poll() {
        try {
          const r = await fetch('/' + cfg.slug + '.json?points=0', { cache: 'no-store' });
          const body = await r.json();
          this.progress = body.progress || null;
          const prev = this.status;
          this.status = body.repo ? body.repo.status : 'error';
          if (prev !== this.status && this.status !== 'processing' && this.status !== 'pending') {
            window.location.reload(); // finished: get the server rendered stats table
            return;
          }
        } catch (e) {
          console.warn('poll failed', e);
        }
        setTimeout(() => this.poll(), 2000);
      },
      async copy() {
        try {
          await navigator.clipboard.writeText(this.markdown);
          this.copied = true;
          setTimeout(() => (this.copied = false), 1500);
        } catch (e) {
          console.warn('clipboard', e);
        }
      },
    },
  }).mount(el);
})();

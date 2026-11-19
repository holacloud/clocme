// Owner page: cards of all repos of a user/organization, filter/sort, and
// live updates while the repos are being analyzed.
(function () {
  const el = document.getElementById('owner-app');
  if (!el) return;
  const initial = JSON.parse(el.dataset.view);

  function compact(n) {
    if (n < 1000) return String(n);
    const units = [['M', 1e6], ['k', 1e3]];
    for (const [u, v] of units) {
      if (n >= v) {
        const x = n / v;
        return (x >= 100 ? Math.round(x) : Math.round(x * 10) / 10) + u;
      }
    }
    return String(n);
  }

  // --- language bar layout helpers ---------------------------------------
  let ctx2d = null;
  function textWidth(text, font) {
    if (!ctx2d) ctx2d = document.createElement('canvas').getContext('2d');
    ctx2d.font = font;
    return ctx2d.measureText(text).width;
  }

  // dark text on light segments, white on dark ones
  function foreground(hex) {
    const m = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex || '');
    if (!m) return '#fff';
    const [r, g, b] = m.slice(1).map((x) => parseInt(x, 16) / 255);
    const lum = 0.2126 * r + 0.7152 * g + 0.0722 * b;
    return lum > 0.6 ? '#1f2328' : '#fff';
  }

  const SEG_PAD = 16, ICON = 16, GAP = 5, SEG_GAP = 2;

  Vue.createApp({
    delimiters: ['${', '}'],
    data() {
      return {
        slug: initial.slug,
        repos: initial.repos || [],
        languages: initial.languages || [],
        code: initial.code || 0,
        ready: initial.ready || 0,
        filter: '',
        sort: 'stars',
        versions: {}, // per repo cache buster, bumped when a chart becomes ready
        barWidth: 0,
      };
    },
    computed: {
      // Decide per segment what fits inside: icon + name + percent, then
      // progressively less. Whatever cannot show its name goes to the legend.
      segments() {
        const langs = this.languages;
        const total = langs.reduce((a, l) => a + l.percent, 0) || 1;
        const usable = Math.max(0, this.barWidth - SEG_GAP * (langs.length - 1));
        const family = getComputedStyle(document.body).fontFamily;
        const nameFont = '600 13px ' + family, pctFont = '12px ' + family;
        return langs.map((l) => {
          const w = (l.percent / total) * usable;
          const name = textWidth(l.name, nameFont);
          const pct = textWidth(l.percent + '%', pctFont);
          const icon = l.icon ? ICON + GAP : 0;
          const options = [
            { showIcon: !!l.icon, showName: true, showPct: true, need: SEG_PAD + icon + name + GAP + pct },
            { showIcon: !!l.icon, showName: true, showPct: false, need: SEG_PAD + icon + name },
            { showIcon: false, showName: true, showPct: false, need: SEG_PAD + name },
            { showIcon: !!l.icon, showName: false, showPct: false, need: SEG_PAD + ICON },
          ];
          const fit = options.find((o) => (o.showIcon || o.showName) && o.need <= w) || { showIcon: false, showName: false, showPct: false };
          return Object.assign({}, l, fit, { fg: foreground(l.color) });
        });
      },
      legend() {
        return this.segments.filter((s) => !s.showPct);
      },
      barLabel() {
        return this.languages.map((l) => l.name + ' ' + l.percent + '%').join(', ');
      },
      pending() {
        return this.repos.filter((r) => r.status === 'pending' || r.status === 'processing').length;
      },
      visible() {
        const f = this.filter.trim().toLowerCase();
        const list = this.repos.filter(
          (r) => !f || r.name.toLowerCase().includes(f) || (r.description || '').toLowerCase().includes(f)
        );
        const by = {
          stars: (a, b) => b.stars - a.stars || b.code - a.code,
          code: (a, b) => b.code - a.code || b.stars - a.stars,
          pushed: (a, b) => new Date(b.pushed_at) - new Date(a.pushed_at),
          name: (a, b) => a.name.localeCompare(b.name),
        }[this.sort];
        return list.slice().sort(by);
      },
    },
    mounted() {
      if (this.pending) setTimeout(() => this.poll(), 3000);
      this.observeBar();
    },
    updated() {
      this.observeBar();
    },
    methods: {
      compact,
      observeBar() {
        const bar = this.$refs.bar;
        if (!bar || bar === this._observed) return;
        this._observed = bar;
        this.barWidth = bar.clientWidth;
        new ResizeObserver(() => (this.barWidth = bar.clientWidth)).observe(bar);
      },
      thumb(r) {
        const v = this.versions[r.name];
        return '/' + r.slug + '.svg?title=0&legend=0&w=400&h=200' + (v ? '&v=' + v : '');
      },
      progressText(r) {
        const p = r.progress;
        if (!p) return 'Starting…';
        if (p.phase === 'sync') return 'Cloning…';
        if (p.total) return 'Commit ' + p.done + ' of ' + p.total;
        return 'Counting…';
      },
      async poll() {
        try {
          const res = await fetch('/' + this.slug + '.json', { cache: 'no-store' });
          const v = await res.json();
          const before = Object.fromEntries(this.repos.map((r) => [r.name, r.has_chart]));
          for (const r of v.repos) {
            if (r.has_chart && !before[r.name]) this.versions[r.name] = Date.now();
          }
          this.repos = v.repos;
          this.languages = v.languages || [];
          this.code = v.code || 0;
          this.ready = v.ready || 0;
        } catch (e) {
          console.warn('poll failed', e);
        }
        if (this.pending) setTimeout(() => this.poll(), 3000);
      },
    },
  }).mount(el);
})();

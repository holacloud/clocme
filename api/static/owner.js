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
      };
    },
    computed: {
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
    },
    methods: {
      compact,
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

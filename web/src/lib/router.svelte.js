// A tiny hash router: #/, #/trade?symbol=AAPL, #/orders, #/orders/<id>, #/ops

function parse() {
  const hash = location.hash.replace(/^#/, '') || '/'
  const [path, query = ''] = hash.split('?')
  const parts = path.split('/').filter(Boolean)
  return { page: parts[0] || 'portfolio', id: parts[1] ? decodeURIComponent(parts[1]) : null, query: new URLSearchParams(query) }
}

export const route = $state(parse())

window.addEventListener('hashchange', () => Object.assign(route, parse()))

export const go = (hash) => { location.hash = hash }

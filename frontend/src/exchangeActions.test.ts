import { toCurl, actionsFor } from './exchangeActions'
import { HTTPExchange } from './api'

function exchange(over: Partial<HTTPExchange> = {}): HTTPExchange {
  return {
    id: 'e1',
    project_id: 'p1',
    name: '',
    origin: 'proxy',
    method: 'GET',
    url: 'https://api.test/v1',
    request_headers: '',
    request_body: '',
    response_headers: '',
    response_body: '',
    created_at: '2026-08-03T00:00:00Z',
    ...over,
  }
}

describe('toCurl', () => {
  it('renders method, url, headers, and body', () => {
    const c = toCurl(
      exchange({
        method: 'POST',
        url: 'https://api.test/login',
        request_headers: 'Content-Type: application/json\nX-Token: abc',
        request_body: '{"u":"a"}',
      }),
    )
    expect(c).toContain("curl -i -X POST 'https://api.test/login'")
    expect(c).toContain("-H 'Content-Type: application/json'")
    expect(c).toContain("-H 'X-Token: abc'")
    expect(c).toContain(`--data '{"u":"a"}'`)
  })

  it('shell-quotes values so an embedded single quote cannot break out', () => {
    const c = toCurl(exchange({ url: "https://x/?q=a'b" }))
    expect(c).toContain(`'https://x/?q=a'\\''b'`)
  })

  it('omits the body line when there is no request body', () => {
    expect(toCurl(exchange())).not.toContain('--data')
  })
})

describe('actionsFor', () => {
  it('hides save-evidence until the exchange has a response (sent_at)', () => {
    const ids = (ex: HTTPExchange) => actionsFor(ex).map((a) => a.id)
    expect(ids(exchange())).not.toContain('save-evidence')
    expect(ids(exchange({ sent_at: '2026-08-03T00:00:01Z' }))).toContain('save-evidence')
  })

  it('always offers replay and copy-curl', () => {
    const ids = actionsFor(exchange()).map((a) => a.id)
    expect(ids).toEqual(expect.arrayContaining(['send-to-replay', 'copy-curl']))
  })
})

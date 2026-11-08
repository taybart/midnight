-- midnight.markup: runtime for midnight's markup elements.
--
-- Elements are lowered to calls of `h(tag, props, ...children)`:
--
--   local { h, render } = require('midnight.markup')
--   print(render(<p class="greeting">hello {name}!</p>))
--
-- `h` builds a tree of nodes, which `render` turns into an HTML string. Text
-- and attribute values are escaped; use `raw` for trusted HTML.
--
-- Plain Lua (5.1+), so it also works w/ the output of `midnight compile`.

local M = {}

local Node = {} -- metatable of elements
local Raw = {} -- metatable of trusted HTML

local VOID = {
  area = true, base = true, br = true, col = true, embed = true, hr = true,
  img = true, input = true, link = true, meta = true, param = true,
  source = true, track = true, wbr = true,
}

-- Elements whose text content is not HTML (so it isn't escaped).
local RAW_TEXT = { script = true, style = true }

local ESCAPES = {
  ['&'] = '&amp;', ['<'] = '&lt;', ['>'] = '&gt;', ['"'] = '&quot;', ["'"] = '&#39;',
}

local function escape(s)
  return (tostring(s):gsub('[&<>"\']', ESCAPES))
end

local function isCallable(v)
  if type(v) == 'function' then return true end
  local mt = getmetatable(v)
  return type(mt) == 'table' and mt.__call ~= nil
end

-- h(tag, props, ...children)
--   tag: string (element), function (component), or nil (fragment)
--
-- Components aren't called here, but when they're rendered (top down, see
-- renderComponent), w/ their props (`props.children` holds the children).
-- `nil`, `false`, and `true` children render nothing, so `{cond && <b/>}`
-- works as expected. They're kept (nil as false) as placeholders, so a
-- condition doesn't change the position (and so the state) of the children
-- after it.
function M.h(tag, props, ...)
  local children, n, args = {}, select('#', ...), { ... }
  for i = 1, n do
    local child = args[i]
    if child == nil then
      child = false
    end
    children[i] = child
  end

  if tag == nil then
    return children
  elseif type(tag) == 'string' then
    return setmetatable({ tag = tag, props = props or {}, children = children }, Node)
  elseif isCallable(tag) then
    props = props or {}
    props.children = children
    return setmetatable({ component = tag, props = props }, Node)
  end

  error('markup: invalid element type (' .. type(tag) .. ')', 2)
end

-- raw(html) marks a string as trusted HTML, which is rendered as is.
function M.raw(html)
  return setmetatable({ tostring(html) }, Raw)
end

-- isElement(v) returns whether `v` was built by `h` (an element or a
-- component).
function M.isElement(v)
  return getmetatable(v) == Node
end

-- map(list, fn) returns a list of `fn(value, index)` (for rendering lists:
-- `{map(items, item -> <li>{item}</li>)}`).
function M.map(list, fn)
  local out = {}
  for i, value in ipairs(list) do
    out[#out + 1] = fn(value, i)
  end
  return out
end

local function sortedKeys(t)
  local keys = {}
  for key in pairs(t) do
    if type(key) == 'string' then keys[#keys + 1] = key end
  end
  table.sort(keys)
  return keys
end

-- `style={{ color = 'red', ['font-size'] = '2em' }}`
local function styleValue(style)
  local parts = {}
  for _, key in ipairs(sortedKeys(style)) do
    parts[#parts + 1] = key .. ': ' .. tostring(style[key])
  end
  return table.concat(parts, '; ')
end

-- `class={{ 'btn', 'btn-lg', active = isActive }}`
local function classValue(class)
  local parts = {}
  for _, name in ipairs(class) do
    if name then parts[#parts + 1] = tostring(name) end
  end
  for _, key in ipairs(sortedKeys(class)) do
    if class[key] then parts[#parts + 1] = key end
  end
  return table.concat(parts, ' ')
end

-- Actions (see `mount`): functions in `on*` attributes, by id. Ids are the
-- id of the component they're in and their position in it (ex. `r:1.2:3#1`),
-- so they don't change when other components do.
local actions = {}
local mounted -- { root = fn, rpc = url, headers = {}, morph = { inline | src } | false }

-- `onclick`, `onClick`, and `on:click` are all `click`.
local function eventName(key)
  local event = key:match('^on:(.+)$') or key:match('^on(%a.*)$')
  return event and event:lower()
end

-- js string literal (single quoted), safe in html attributes and <script>
local function jsString(s)
  return "'" .. tostring(s):gsub('[%c\'"\\<>&]', function(c)
    return string.format('\\u%04x', c:byte())
  end) .. "'"
end

-- Component instances. Each component rendered gets an id from its position
-- in the tree (its parent's id, its index among its siblings or its `key`,
-- and which component it is), which is what its state is kept by (see
-- `state`). Instances that aren't rendered again are dropped after each
-- render.
local instances = {} -- id -> { hooks = {} }
local seen = {} -- ids rendered in the current render
local current -- { instance, hook } of the component being rendered

-- What each component rendered in the current render (for `dispatch` to send
-- only what changed): id -> {
--   html,   -- its html
--   shell,  -- its html w/ child components replaced by `<!--id-->`, so it
--           -- only changes if the component's own markup does
--   parent, -- id of the parent component ('r' for the root)
--   tag,    -- tag of its root element, if it has one (w/ its id in
--           -- `data-midnight`, so the client can find it)
-- }
local rendered = {}
-- `rendered` of the last (outermost) render, and the root's shell
local lastRender

-- The component being rendered (or the root): { id, children, nextAction },
-- where children are the indices of child components' html in its buffer.
local frame

local componentIds = setmetatable({}, { __mode = 'k' })
local nextComponentId = 0
local function componentId(fn)
  local id = componentIds[fn]
  if not id then
    nextComponentId = nextComponentId + 1
    id = nextComponentId
    componentIds[fn] = id
  end
  return id
end

-- slot is the path of the `i`th child of `path`.
local function slot(path, i, child)
  if getmetatable(child) == Node and child.props.key ~= nil then
    -- no spaces, ids are space separated in `data-midnight`
    return path .. '.k' .. tostring(child.props.key):gsub('%s', '_')
  end
  return path .. '.' .. i
end

local renderTo

-- `owners` are the ids of the components whose root is this element (in
-- `data-midnight` on mounted pages).
local function renderElement(buf, node, path, owners)
  local tag, props = node.tag, node.props
  buf[#buf + 1] = '<' .. tag
  if owners and mounted then
    buf[#buf + 1] = ' data-midnight="' .. escape(table.concat(owners, ' ')) .. '"'
    for _, owner in ipairs(owners) do
      if rendered[owner] then
        rendered[owner].tag = tag
      end
    end
  end

  -- Sorted, so output is deterministic.
  for _, key in ipairs(sortedKeys(props)) do
    local value = props[key]
    local event = type(value) == 'function' and mounted and eventName(key)
    if event then
      frame.nextAction = frame.nextAction + 1
      local id = frame.id .. '#' .. frame.nextAction
      actions[id] = value
      buf[#buf + 1] = ' on' .. event .. '="midnight.rpc(' .. jsString(id) .. ', event)"'
    elseif key == 'children' or key == 'key' or value == nil or value == false or type(value) == 'function' then
      -- skipped (functions are event handlers, which only mean something in
      -- the browser, see `mount`)
    elseif value == true then
      buf[#buf + 1] = ' ' .. key
    else
      if type(value) == 'table' and getmetatable(value) == nil then
        value = key == 'style' and styleValue(value) or classValue(value)
      end
      buf[#buf + 1] = ' ' .. key .. '="' .. escape(value) .. '"'
    end
  end

  buf[#buf + 1] = '>'

  if VOID[tag] then
    for _, child in ipairs(node.children) do
      if child ~= false and child ~= true then
        error('markup: <' .. tag .. '> can\'t have children', 0)
      end
    end
    return
  end

  for i, child in ipairs(node.children) do
    renderTo(buf, child, RAW_TEXT[tag], slot(path, i, child))
  end
  buf[#buf + 1] = '</' .. tag .. '>'
end

-- shell is `buf` w/ the html of child components (at `children` indices)
-- replaced by markers.
local function shell(buf, children)
  for i, id in pairs(children) do
    buf[i] = '<!--' .. id .. '-->'
  end
  return table.concat(buf)
end

local function renderComponent(buf, node, rawText, path, owners)
  local id = path .. ':' .. componentId(node.component)
  seen[id] = true
  local instance = instances[id]
  if not instance then
    instance = { hooks = {} }
    instances[id] = instance
  end
  local entry = { parent = frame.id }
  rendered[id] = entry

  local parent, parentFrame = current, frame
  current = { instance = instance, hook = 0 }
  frame = { id = id, children = {}, nextAction = 0 }
  local ok, result = pcall(node.component, node.props)
  current = parent
  if not ok then
    error(result, 0)
  end

  local ids = { id }
  for i, owner in ipairs(owners or {}) do
    ids[i + 1] = owner
  end
  local own = {}
  renderTo(own, result, rawText, id, ids)

  entry.html = table.concat(own)
  entry.shell = shell(own, frame.children)
  frame = parentFrame

  buf[#buf + 1] = entry.html
  frame.children[#buf] = id
end

function renderTo(buf, node, rawText, path, owners)
  local t = type(node)

  if node == nil or node == false or node == true then
    return
  elseif t == 'string' then
    buf[#buf + 1] = rawText and node or escape(node)
  elseif t == 'number' then
    buf[#buf + 1] = tostring(node)
  elseif t == 'table' then
    local mt = getmetatable(node)
    if mt == Node and node.component then
      renderComponent(buf, node, rawText, path, owners)
    elseif mt == Node then
      renderElement(buf, node, path, owners)
    elseif mt == Raw then
      buf[#buf + 1] = node[1]
    elseif mt ~= nil and mt.__tostring then
      renderTo(buf, tostring(node), rawText, path)
    else -- list of children (ex. a fragment, or the result of `map`)
      for i = 1, #node do
        renderTo(buf, node[i], rawText, slot(path, i, node[i]))
      end
    end
  elseif t == 'function' then -- lazy child
    renderTo(buf, node(), rawText, path, owners)
  else
    error('markup: can\'t render a ' .. t, 0)
  end
end

local renderDepth, nestedRenders = 0, 0

-- render(node) renders a node (or string, list, etc.) to an HTML string.
function M.render(node)
  -- renders can nest (ex. `tostring(<b/>)` in a component), only the
  -- outermost one starts / finishes a render
  local outermost = renderDepth == 0
  local root = 'r'
  local parent, parentFrame = current, frame
  if outermost then
    actions, seen, rendered, nestedRenders = {}, {}, {}, 0
  else
    nestedRenders = nestedRenders + 1
    root = 'n' .. nestedRenders
  end
  frame = { id = root, children = {}, nextAction = 0 }

  renderDepth = renderDepth + 1
  local buf = {}
  local ok, err = pcall(renderTo, buf, node, false, root)
  renderDepth = renderDepth - 1
  local rootFrame = frame
  current, frame = parent, parentFrame
  if not ok then
    error(err, 0)
  end

  local html = table.concat(buf)
  if outermost then
    for id in pairs(instances) do
      if not seen[id] then
        instances[id] = nil
      end
    end
    lastRender = { instances = rendered, shell = shell(buf, rootFrame.children) }
  end
  return html
end

-- state(init) returns the state of the component being rendered: a table
-- that's kept between renders (ex. after actions on a mounted page). `init`
-- (a table, or a function returning one) is only used the first time. A
-- component can have several (they're kept by call order, so don't call
-- `state` conditionally).
--
--   local Counter = () -> {
--     local s = state({ count = 0 })
--     return <button onclick={() -> { s.count += 1 }}>{s.count}</button>
--   }
function M.state(init)
  if not current then
    error('markup: state must be called while rendering a component', 2)
  end
  current.hook = current.hook + 1
  local hooks = current.instance.hooks
  local value = hooks[current.hook]
  if value == nil then
    if type(init) == 'function' then
      init = init()
    end
    value = init == nil and {} or init
    if type(value) ~= 'table' then
      error('markup: state must be a table (got ' .. type(value) .. ')', 2)
    end
    hooks[current.hook] = value
  end
  return value
end

-- Scoped styles. `css(rules)` turns lua tables into css, w/ class names
-- generated from a hash of the rules (so they're the same everywhere the rules
-- are), and returns them by name:
--
--   local s = css({
--     card = {
--       padding = '1rem',
--       backgroundColor = '#222',        -- (or background_color)
--       ['&:hover'] = { borderColor = 'gold' }, -- `&` is the class
--       ['$title'] = { margin = 0 },     -- `$name` is another class here
--       ['img'] = { width = '100%' },    -- descendants
--       ['@media (max-width: 600px)'] = { padding = '.5rem' },
--     },
--     title = { color = 'gold' },
--   })
--   <section class={s.card}><h2 class={s.title}>hi</h2></section>
--
-- `document` includes all the styles (in a <style> in the <head>, which is
-- added to an <html> w/o one, or at the start), and `styles()` returns them, to put them somewhere else (ex. in a
-- shadow root).

local sheets = {} -- css text of each `css` call (in order, no duplicates)
local sheetHashes = {}
local sentSheets = 0 -- sheets in the last document of the mounted page

local BASE36 = '0123456789abcdefghijklmnopqrstuvwxyz'
local function hash(s)
  local h = 5381
  for i = 1, #s do
    h = (h * 33 + s:byte(i)) % 4294967296
  end
  local out = ''
  repeat
    local digit = h % 36
    out = BASE36:sub(digit + 1, digit + 1) .. out
    h = (h - digit) / 36
  until h == 0
  return out
end

-- fontSize / font_size -> font-size
local function property(name)
  return (name:gsub('_', '-'):gsub('(%l)(%u)', function(a, b)
    return a .. '-' .. b:lower()
  end))
end

-- Generated class names end w/ HASH until the hash (of the css w/ them) is
-- known. (Not \0, lua 5.1 patterns can't have it.)
local HASH = '\1'

local function cssRule(selector, rules, out)
  local declarations, nested = {}, {}
  for _, key in ipairs(sortedKeys(rules)) do
    local value = rules[key]
    if type(value) == 'table' then
      nested[#nested + 1] = key
    else
      declarations[#declarations + 1] = property(key) .. ': ' .. tostring(value) .. ';'
    end
  end
  if #declarations > 0 then
    out[#out + 1] = selector .. ' { ' .. table.concat(declarations, ' ') .. ' }'
  end

  for _, key in ipairs(nested) do
    local refs = key:gsub('%$([%w_]+)', '.%1-' .. HASH)
    if key:sub(1, 1) == '@' then
      local inner = {}
      cssRule(selector, rules[key], inner)
      out[#out + 1] = key .. ' { ' .. table.concat(inner, ' ') .. ' }'
    elseif refs:find('&', 1, true) then
      cssRule((refs:gsub('&', function() return selector end)), rules[key], out)
    else
      cssRule(selector .. ' ' .. refs, rules[key], out)
    end
  end
end

function M.css(rules)
  assert(type(rules) == 'table', 'markup: css requires a table of rules, by class name')
  local out = {}
  for _, name in ipairs(sortedKeys(rules)) do
    assert(type(rules[name]) == 'table', 'markup: css rules for ' .. name .. ' must be a table')
    cssRule('.' .. name .. '-' .. HASH, rules[name], out)
  end
  local text = table.concat(out, '\n')
  local h = hash(text)

  if not sheetHashes[h] then
    sheetHashes[h] = true
    sheets[#sheets + 1] = (text:gsub(HASH, h))
  end

  local names = {}
  for _, name in ipairs(sortedKeys(rules)) do
    names[name] = name .. '-' .. h
  end
  return names
end

-- styles() returns all the styles from `css`, as a <style> (or '' if none).
function M.styles()
  if #sheets == 0 then
    return ''
  end
  return '<style data-midnight-css>\n' .. table.concat(sheets, '\n') .. '\n</style>'
end

-- Client side of actions: calls the rpc endpoint w/ the action id and a
-- summary of the event, then updates the page w/ the response (see
-- `dispatch`): each `<template data-midnight-target="id">` replaces the component
-- w/ that id, otherwise it's the whole page. Updates are morphed (w/
-- idiomorph, see `mount`), or swapped if it isn't loaded. Calls are queued, so
-- they run in order. If the server state is gone (ex. after a restart), or the
-- page is out of sync, it reloads.
local CLIENT = [[
(() => {
  const cfg = %s
  let queue = Promise.resolve()
  // submitted is the focused input of a submitted form, and its value then
  const morph = (from, to, submitted) => {
    if (window.Idiomorph) {
      // keeps focus, selection, and what's being typed in the focused input.
      // Only for inputs: idiomorph skips the focused element's children, so
      // ex. a clicked (focused) button's text would never update.
      const active = document.activeElement
      let typing = !!active && active.matches('input, textarea, select, [contenteditable]')
      // a form submitted w/ Enter sent what was typed, so the server's markup
      // wins (ex. clears the input), like when its button is clicked. Unless
      // more was typed while the action ran.
      if (submitted && submitted.el === active && active.value === submitted.value) typing = false
      Idiomorph.morph(from, to, { ignoreActiveValue: typing })
    } else {
      from.replaceWith(to)
    }
  }
  const summarize = (e) => {
    const t = e.target || {}
    const data = { type: e.type }
    if (typeof t.value === 'string') data.value = t.value
    if (t.type === 'checkbox' || t.type === 'radio') data.checked = t.checked
    if (typeof e.key === 'string') data.key = e.key
    if (e.type === 'submit') {
      e.preventDefault()
      data.form = Object.fromEntries(new FormData(t))
    }
    return data
  }
  window.midnight = {
    rpc(id, e) {
      const data = summarize(e)
      const active = document.activeElement
      const submitted = e.type === 'submit' && active && e.target.contains(active)
        ? { el: active, value: active.value }
        : null
      queue = queue.then(async () => {
        const res = await fetch(cfg.rpc ?? location.pathname + location.search, {
          method: 'POST',
          headers: { ...cfg.headers, 'Content-Type': 'application/json' },
          body: JSON.stringify({ id, data }),
        })
        if (res.status === 410) return location.reload()
        if (!res.ok) throw new Error(`midnight.rpc ${id}: ${res.status} ${await res.text()}`)
        const html = await res.text()
        const page = new DOMParser().parseFromString(html, 'text/html')
        const fragments = page.querySelectorAll('template[data-midnight-target]')
        if (fragments.length === 0) {
          if (html.trim() !== '') morph(document.body, page.body, submitted)
          return
        }
        for (const fragment of fragments) {
          const id = CSS.escape(fragment.dataset.midnightTarget)
          const target = document.querySelector(`[data-midnight~="${id}"]`)
          const next = fragment.content.firstElementChild
          if (!target || !next) return location.reload()
          morph(target, next, submitted)
        }
      }).catch(console.error)
    },
  }
})()
]]

-- idiomorph (https://github.com/bigskysoftware/idiomorph) is vendored as
-- `midnight.markup.idiomorph` (midnight preloads it, elsewhere put it next to this
-- file). If it's missing, it's loaded from a cdn instead.
local MORPH_CDN = 'https://unpkg.com/idiomorph@0.8.0/dist/idiomorph.min.js'

local function vendoredMorph()
  local ok, source = pcall(require, 'midnight.markup.idiomorph')
  if ok and type(source) == 'string' then
    return { inline = source }
  end
  return { src = MORPH_CDN }
end


-- mount(root, { rpc, headers, morph }) renders `root()` as the page, and
-- makes functions in `on*` attributes work: they're registered as actions,
-- and clicking (etc.) POSTs `{ id, data }` (json) to the `rpc` url (or w/o
-- one, to the page's own url, as the browser sees it), which should respond
-- w/ `dispatch(id, data)` (what changed on the page).
-- `headers` are sent along (ex. to find this page's state on the server). A
-- 410 (gone) response reloads the page. `morph` is how updates are applied:
-- by default w/ idiomorph (inlined in the page, see `vendoredMorph`), a url
-- to load it from instead, or false to swap elements (loses focus, etc.).
--
-- The host has to keep this lua state alive between the page and its actions
-- (ex. a rest `page` handler, which does all of this for you).
function M.mount(root, opts)
  opts = opts or {}
  assert(type(root) == 'function', 'markup: mount requires a function')
  assert(opts.rpc == nil or type(opts.rpc) == 'string', 'markup: mount: rpc must be a url (a string)')
  local morph = opts.morph
  if morph == nil then
    morph = vendoredMorph()
  elseif type(morph) == 'string' then
    morph = { src = morph }
  end
  mounted = { root = root, rpc = opts.rpc, headers = opts.headers or {}, morph = morph }
  return root()
end

-- Root elements that can't be replaced on their own (or be in a <template>).
local DOCUMENT_TAGS = { html = true, head = true, body = true }

-- lastIndex returns where the last `needle` is in `s` (a plain search, which is
-- much faster than a pattern anchored at the end).
local function lastIndex(s, needle)
  local last
  local i = s:find(needle, 1, true)
  while i do
    last = i
    i = s:find(needle, i + 1, true)
  end
  return last
end

-- A component's html (which has its children's html in it) is only needed to
-- send what changed in a response, comparing renders only needs their shells.
-- So once a response is made, its html is dropped (a page w/ a lot of nested
-- components would otherwise keep their html many times over).
local dispatching = false
local function forgetHTML()
  if lastRender then
    for _, entry in pairs(lastRender.instances) do
      entry.html = nil
    end
  end
end

-- changed returns the components that changed between two renders, as
-- `<template data-midnight-target="id">html</template>`s, or nil if the whole
-- page has to be sent.
local function changed(before, after)
  if before.shell ~= after.shell then
    return nil -- something outside of any component
  end

  local targets = {}
  for id, entry in pairs(after.instances) do
    local previous = before.instances[id]
    -- (new / removed components change their parent's shell)
    if id:sub(1, 1) == 'r' and previous and previous.shell ~= entry.shell then
      -- w/o a root element of its own, it's replaced w/ its parent
      local target = id
      while target ~= 'r' and not after.instances[target].tag do
        target = after.instances[target].parent
      end
      if target == 'r' or DOCUMENT_TAGS[after.instances[target].tag] then
        return nil
      end
      targets[target] = true
    end
  end

  -- components replaced w/ a parent don't need to be sent on their own
  local ids = {}
  for id in pairs(targets) do
    local parent, covered = after.instances[id].parent, false
    while parent ~= 'r' and not covered do
      covered = targets[parent] or false
      parent = after.instances[parent].parent
    end
    if not covered then
      ids[#ids + 1] = id
    end
  end
  table.sort(ids)

  local out = {}
  for _, id in ipairs(ids) do
    out[#out + 1] = '<template data-midnight-target="' .. escape(id) .. '">' .. after.instances[id].html .. '</template>'
  end
  return table.concat(out)
end

-- dispatch(id, data) runs an action w/ the event `data` sent by the client,
-- and returns what changed on the page as a response table ({ headers, body },
-- ex. for a rest handler): the components that changed (their html, by id),
-- or the whole page when something outside of a component (with its own root
-- element) changed. Nothing changed is an empty body.
function M.dispatch(id, data)
  assert(mounted, 'markup: dispatch requires a mounted page (see mount)')
  local headers = { ['Content-Type'] = 'text/html; charset=utf-8' }
  local action = actions[id]
  if not action then
    -- a stale action, ex. a second click on a button the first one removed:
    -- the client is out of date, so it gets the whole page
    return { headers = headers, body = M.document(mounted.root()) }
  end

  local before, sent = lastRender, sentSheets
  dispatching = true
  local ok, page = pcall(function()
    action(data or {})
    return M.document(mounted.root())
  end)
  dispatching = false
  if not ok then
    error(page, 0)
  end

  -- new styles (from `css` calls the page hasn't seen) need the whole page
  local body = before and #sheets == sent and changed(before, lastRender) or page
  forgetHTML()
  return { headers = headers, body = body }
end

-- isInteractive returns whether the last render of a mounted page had any
-- actions (if not, the page is static: it never calls the server back, so
-- there's nothing to keep around for it).
function M.isInteractive()
  return mounted ~= nil and next(actions) ~= nil
end

-- document(node) is like render, but prefixed w/ `<!DOCTYPE html>`. For a
-- mounted page w/ actions, it includes the client script.
function M.document(node)
  local html = M.render(node)
  local styles = M.styles()
  if styles ~= '' then
    local headEnd = html:find('</head>', 1, true)
    local _, htmlStart = html:find('^%s*<html[^>]*>')
    if headEnd then
      html = html:sub(1, headEnd - 1) .. styles .. html:sub(headEnd)
    elseif htmlStart then -- <html> w/o a <head>
      html = html:sub(1, htmlStart) .. '<head>' .. styles .. '</head>' .. html:sub(htmlStart + 1)
    else
      html = styles .. html
    end
  end
  html = '<!DOCTYPE html>' .. html
  if mounted then
    sentSheets = #sheets
    if not dispatching then
      forgetHTML()
    end
  end
  if not M.isInteractive() then
    return html
  end

  local headers = {}
  for _, key in ipairs(sortedKeys(mounted.headers)) do
    headers[#headers + 1] = jsString(key) .. ': ' .. jsString(mounted.headers[key])
  end
  local rpc = mounted.rpc and jsString(mounted.rpc) or 'null'
  local cfg = '{ rpc: ' .. rpc .. ', headers: { ' .. table.concat(headers, ', ') .. ' } }'
  local script = '<script>' .. CLIENT:format(cfg) .. '</script>'
  if mounted.morph and mounted.morph.src then
    script = '<script src="' .. escape(mounted.morph.src) .. '"></script>' .. script
  elseif mounted.morph then
    script = '<script>' .. mounted.morph.inline .. '</script>' .. script
  end
  local bodyEnd = lastIndex(html, '</body>') or lastIndex(html, '</html>')
  if bodyEnd then
    return html:sub(1, bodyEnd - 1) .. script .. html:sub(bodyEnd)
  end
  return html .. script
end

M.escape = escape

Node.__tostring = M.render
Raw.__tostring = function(self) return self[1] end

return M

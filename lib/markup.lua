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

-- Go helpers midnight passes when it loads this module (w/ plain `require`,
-- `...` is the module name, and lua versions are used instead).
local native = type((...)) == 'table' and (...) or {}

-- metatable of elements: { tag, props, children } (an element), or
-- { component, props } (a component, w/ its children in props.children).
-- Arrays, not { tag = ..., ... }: a table w/ string keys costs a lot more.
local Node = {}
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

local find, gsub = string.find, string.gsub

local function escape(s)
  if type(s) ~= 'string' then s = tostring(s) end
  -- most text has nothing to escape: find is cheaper than gsub's copy
  if not find(s, '[&<>"\']') then return s end
  return (gsub(s, '[&<>"\']', ESCAPES))
end

local function is_callable(v)
  if type(v) == 'function' then return true end
  local mt = getmetatable(v)
  return type(mt) == 'table' and mt.__call ~= nil
end

-- h(tag, props, ...children)
--   tag: string (element), function (component), or nil (fragment)
--
-- Components aren't called here, but when they're rendered (top down, see
-- render_component), w/ their props (`props.children` holds the children).
-- `nil`, `false`, and `true` children render nothing, so `{cond && <b/>}`
-- works as expected. They're kept (nil as false) as placeholders, so a
-- condition doesn't change the position (and so the state) of the children
-- after it.
function M.h(tag, props, ...)
  local children = { ... }
  for i = 1, select('#', ...) do
    if children[i] == nil then
      children[i] = false
    end
  end

  if tag == nil then
    -- fragments are h(nil, nil, ...): props mean a component that's nil
    if props ~= nil then
      error('markup: component is nil (not defined, or not imported?)', 2)
    end
    return children
  elseif type(tag) == 'string' then
    return setmetatable({ tag, props or {}, children }, Node)
  elseif is_callable(tag) then
    props = props or {}
    props.children = children
    return setmetatable({ tag, props }, Node)
  end

  error('markup: invalid element type (' .. type(tag) .. ')', 2)
end

-- raw(html) marks a string as trusted HTML, which is rendered as is.
function M.raw(html)
  return setmetatable({ tostring(html) }, Raw)
end

-- is_element(v) returns whether `v` was built by `h` (an element or a
-- component).
function M.is_element(v)
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

local NO_KEYS = {}

-- (callers only read the keys, so tables w/o any share NO_KEYS)
local function sorted_keys(t)
  if next(t) == nil then return NO_KEYS end
  local keys, n = {}, 0
  for key in pairs(t) do
    if type(key) == 'string' then n = n + 1; keys[n] = key end
  end
  if n > 1 then table.sort(keys) end
  return keys
end

-- JSON data (for the page's own js) ----------------------------------------
--
-- A `<script type="application/json">`'s children are encoded as JSON when
-- it's rendered, so a page can hand data to its js:
--
--   <script type="application/json" id="repos">{repos}</script>
--   <Data id="repos" value={repos}/>                 -- the same
--   JSON.parse(document.getElementById('repos').textContent)
--
-- The JSON never has a `<`, `>`, or `&` (they're \u escaped), so nothing in
-- the data can close the <script>. json(v) / json_text(s) values are already
-- encoded, and pass through as is.

local Json = {} -- metatable of encoded JSON (safe in a <script>)

-- array(t) / object(t) mark how a table is encoded when that can't be told
-- from its keys: an empty table could be either, and is an error unmarked.
local shapes = setmetatable({}, { __mode = 'k' })
function M.array(t) t = t or {}; shapes[t] = 'array'; return t end
function M.object(t) t = t or {}; shapes[t] = 'object'; return t end

local JSON_ESCAPES = {
  ['"'] = '\\"', ['\\'] = '\\\\', ['\b'] = '\\b', ['\f'] = '\\f',
  ['\n'] = '\\n', ['\r'] = '\\r', ['\t'] = '\\t',
  ['<'] = '\\u003c', ['>'] = '\\u003e', ['&'] = '\\u0026',
}
local function json_escape(c)
  return JSON_ESCAPES[c] or string.format('\\u%04x', c:byte())
end

local function json_string(s)
  if find(s, '[%c"\\<>&\226]') then
    s = gsub(s, '[%c"\\<>&]', json_escape)
    -- valid JSON, but line terminators in js before ES2019
    s = gsub(gsub(s, '\226\128\168', '\\u2028'), '\226\128\169', '\\u2029')
  end
  return '"' .. s .. '"'
end

local encode_json

local function json_table(t, out, path, seen)
  local mt = getmetatable(t)
  if mt == Json then
    out[#out + 1] = t[1]
    return
  elseif mt == Node or mt == Raw then
    error('markup: json: can\'t encode an element' .. path, 0)
  end
  if seen[t] then
    error('markup: json: cycle' .. path, 0)
  end
  seen[t] = true

  local n, count, strings = #t, 0, true
  for k in pairs(t) do
    count = count + 1
    if type(k) ~= 'string' then strings = false end
  end
  local shape = shapes[t]
  if not shape then
    if count == 0 then
      error('markup: json: empty table' .. path .. ' could be [] or {}: use array(t) or object(t)', 0)
    elseif count == n then
      shape = 'array'
    elseif strings then
      shape = 'object'
    else
      error('markup: json: table' .. path .. ' has keys that aren\'t 1..n or all strings', 0)
    end
  end

  if shape == 'array' then
    if count ~= n then
      error('markup: json: array' .. path .. ' isn\'t a sequence', 0)
    end
    out[#out + 1] = '['
    for i = 1, n do
      if i > 1 then out[#out + 1] = ',' end
      encode_json(t[i], out, path .. '[' .. i .. ']', seen)
    end
    out[#out + 1] = ']'
  else
    if not strings then
      error('markup: json: object' .. path .. ' has non-string keys', 0)
    end
    local keys = {}
    for k in pairs(t) do keys[#keys + 1] = k end
    table.sort(keys) -- deterministic output
    out[#out + 1] = '{'
    for i, k in ipairs(keys) do
      if i > 1 then out[#out + 1] = ',' end
      out[#out + 1] = json_string(k)
      out[#out + 1] = ':'
      encode_json(t[k], out, path .. '.' .. k, seen)
    end
    out[#out + 1] = '}'
  end
  seen[t] = nil
end

function encode_json(v, out, path, seen)
  local t = type(v)
  if v == nil then
    out[#out + 1] = 'null'
  elseif t == 'boolean' then
    out[#out + 1] = v and 'true' or 'false'
  elseif t == 'number' then
    if v ~= v or v == math.huge or v == -math.huge then
      error('markup: json: ' .. tostring(v) .. path .. ' isn\'t a JSON number', 0)
    end
    if v == math.floor(v) and v > -2^53 and v < 2^53 then
      out[#out + 1] = string.format('%d', v)
    else
      local short = string.format('%.14g', v)
      out[#out + 1] = tonumber(short) == v and short or string.format('%.17g', v)
    end
  elseif t == 'string' then
    out[#out + 1] = json_string(v)
  elseif t == 'table' then
    json_table(v, out, path, seen)
  else
    error('markup: json: can\'t encode a ' .. t .. path, 0)
  end
end

-- json(v) encodes a value as JSON (see above), for a JSON <script>.
function M.json(v)
  if getmetatable(v) == Json then return v end
  local out = {}
  encode_json(v, out, '', {})
  return setmetatable({ table.concat(out) }, Json)
end

-- json_text(s) is JSON encoded by the host (ex. Go's encoding/json), trusted to
-- be valid and to have no `<` (like raw, it's rendered as is).
function M.json_text(s)
  return setmetatable({ tostring(s) }, Json)
end

-- <Data id="x" value={v}/> is <script type="application/json" id="x">{v}</script>.
function M.Data(props)
  return M.h('script', { type = 'application/json', id = props.id }, M.json(props.value))
end

-- <script define:vars={{ name = value }}> declares each name in the script, as if
-- it started w/ `const name = <value>;`:
--
--   <script define:vars={{ repos = props.repos }}>
--     const hits = repos.filter(r => r.name.includes(q))
--   </script>
--
-- Values are encoded like json(v) and parsed in the browser
-- (JSON.parse('...'), which is faster than a js literal for big data, and
-- has no `__proto__` surprises). The JSON has no `<`, and is in a js string,
-- so nothing in the data can close the <script>.
local JS_RESERVED = {}
for word in ([[break case catch class const continue debugger default delete do
  else enum export extends false finally for function if import in instanceof
  new null return super switch this throw true try typeof var void while with
  yield let static implements interface package private protected public
  await arguments eval undefined NaN Infinity]]):gmatch('%S+') do
  JS_RESERVED[word] = true
end

local function define_vars(data)
  if type(data) ~= 'table' or getmetatable(data) ~= nil then
    error('markup: <script define:vars={...}> takes a table of names to values', 0)
  end
  local names = {}
  for name in pairs(data) do
    if type(name) ~= 'string' or not find(name, '^[%a_$][%w_$]*$') or JS_RESERVED[name] then
      error('markup: <script define:vars={...}>: ' .. tostring(name) .. ' isn\'t a js variable name', 0)
    end
    names[#names + 1] = name
  end
  table.sort(names)
  local out = {}
  for _, name in ipairs(names) do
    local ok, encoded = pcall(M.json, data[name])
    if not ok then
      error((gsub(encoded, '^markup: json: ', 'markup: <script define:vars={...}>: ' .. name .. ': ')), 0)
    end
    -- in a '...' js string, JSON only needs \ and ' escaped (it has no
    -- line terminators or `<`)
    local js = gsub(gsub(encoded[1], '\\', '\\\\'), "'", "\\'")
    out[#out + 1] = 'const ' .. name .. " = JSON.parse('" .. js .. "');\n"
  end
  return table.concat(out)
end

-- (an import map's JSON too: `{{ imports = { ... } }}` is encoded, and one
-- written as JSON text passes through as is)
local function is_json_script(tag, props)
  return tag == 'script' and (props.type == 'application/json' or props.type == 'importmap')
end

-- `style={{ color = 'red', ['font-size'] = '2em' }}`
local function style_value(style)
  local parts = {}
  for _, key in ipairs(sorted_keys(style)) do
    parts[#parts + 1] = key .. ': ' .. tostring(style[key])
  end
  return table.concat(parts, '; ')
end

-- cx(...) joins class names: strings and numbers are kept, `false` / `nil`
-- are dropped, and a table is flattened (array entries, then truthy string
-- keys), so conditional and nested classes compose:
--
--   class={cx('btn', active && 'on', { 'a', 'b' })}
--   class={{ 'btn', 'btn-lg', active = isActive }}   -- the class attribute's own table form
local function append_class(parts, v)
  if v == nil or v == false then
    return
  end
  local t = type(v)
  if t == 'string' or t == 'number' then
    parts[#parts + 1] = tostring(v)
  elseif t == 'table' and getmetatable(v) == nil then
    for _, name in ipairs(v) do
      append_class(parts, name)
    end
    for _, key in ipairs(sorted_keys(v)) do
      if v[key] then parts[#parts + 1] = key end
    end
  end
end

function M.cx(...)
  local parts = {}
  for i = 1, select('#', ...) do
    append_class(parts, (select(i, ...)))
  end
  return table.concat(parts, ' ')
end

-- `class={{ 'btn', 'btn-lg', active = isActive }}`
local function class_value(class)
  return M.cx(class)
end

-- What's kept for a page between renders. `mount` / `dispatch` use the
-- default one; each `session()` has its own, so one lua state can serve many
-- pages (ex. a bot's panel for each of its viewers).
local function new_page()
  return {
    -- functions in `on*` attributes, by id (see below)
    actions = {},
    -- { root = fn, rpc = url, headers = {}, morph = { inline | src } | false,
    --   attrs = 'data' (sessions: actions as `data-mn-<event>` attributes) }
    mounted = nil,
    -- component instances, by id (see below): id -> { hooks = {} }
    instances = {},
    -- `rendered` of the last (outermost) render, and the root's shell
    last_render = nil,
    -- sheets in the last document of the mounted page
    sent_sheets = 0,
  }
end
-- The page being rendered.
local P = new_page()

-- Actions (see `mount`): functions in `on*` attributes, by id. Ids are the
-- id of the component they're in and their position in it (ex. `r:1.2:3#1`),
-- so they don't change when other components do.

-- `onclick`, `onClick`, and `on:click` are all `click`.
local function event_name(key)
  local event = key:match('^on:(.+)$') or key:match('^on(%a.*)$')
  return event and event:lower()
end

-- js string literal (single quoted), safe in html attributes and <script>
local function js_string(s)
  return "'" .. tostring(s):gsub('[%c\'"\\<>&]', function(c)
    return string.format('\\u%04x', c:byte())
  end) .. "'"
end

-- Component instances. Each component rendered gets an id from its position
-- in the tree (its parent's id, its index among its siblings or its `key`,
-- and which component it is), which is what its state is kept by (see
-- `state`). Instances that aren't rendered again are dropped after each
-- render.
local seen = {} -- ids rendered in the current render
local libs, lib_seen = {}, {} -- element libraries used in the current render (see `elements`)
local current -- { instance, hook } of the component being rendered

-- What each component rendered in the current render (for `dispatch` to send
-- only what changed): id -> {
--   own,    -- its buffer (see flatten), for its html
--   shell,  -- its html w/ child components replaced by `<!--id-->`, so it
--           -- only changes if the component's own markup does
--   parent, -- id of the parent component ('r' for the root)
--   tag,    -- tag of its root element, if it has one (w/ its id in
--           -- `data-midnight`, so the client can find it)
-- }
local rendered = {}
-- The buffers w/ a `data-midnight` attribute in the current render: buffer ->
-- the attribute's index (see flatten).
local marked = {}
-- `rendered` of the last (outermost) render, and the root's shell

-- The component being rendered (or the root): { id, children, next_action },
-- where children are the indices of child components' html in its buffer.
local frame

local component_ids = setmetatable({}, { __mode = 'k' })
local next_component_id = 0
local function component_id(fn)
  local id = component_ids[fn]
  if not id then
    next_component_id = next_component_id + 1
    id = next_component_id
    component_ids[fn] = id
  end
  return id
end

-- slot is the path of the `i`th child of `path`.
local function slot(path, i, child)
  if getmetatable(child) == Node and child[2].key ~= nil then
    -- no spaces, ids are space separated in `data-midnight`
    return path .. '.k' .. tostring(child[2].key):gsub('%s', '_')
  end
  return path .. '.' .. i
end

local render_to

-- `owners` are the ids of the components whose root is this element (in
-- `data-midnight` on mounted pages).
local function render_element(buf, node, path, owners)
  local tag, props = node[1], node[2]
  local vars = props['define:vars']
  if vars ~= nil and tag ~= 'script' then
    error('markup: define:vars only works on a <script> (it\'s on a <' .. tag .. '>)', 0)
  end
  local prelude = vars ~= nil and vars ~= false and define_vars(vars)
  buf[#buf + 1] = '<' .. tag
  if owners and P.mounted then
    buf[#buf + 1] = ' data-midnight="' .. escape(table.concat(owners, ' ')) .. '"'
    -- (owners are passed to the first element of a component, so there's
    -- one per buffer, see flatten)
    marked[buf] = #buf
    for _, owner in ipairs(owners) do
      if rendered[owner] then
        rendered[owner].tag = tag
      end
    end
  end

  -- Sorted, so output is deterministic.
  local keys = sorted_keys(props)
  for k = 1, #keys do
    local key = keys[k]
    local value = props[key]
    local event = type(value) == 'function' and P.mounted and event_name(key)
    if event then
      frame.next_action = frame.next_action + 1
      local id = frame.id .. '#' .. frame.next_action
      P.actions[id] = value
      if P.mounted.attrs == 'data' then
        -- for hosts w/ their own client (ex. one that sanitizes the html,
        -- which drops `on*` attributes): the event's action id, nothing to run
        buf[#buf + 1] = ' data-mn-' .. event:gsub('[^%w-]', '') .. '="' .. escape(id) .. '"'
      else
        buf[#buf + 1] = ' on' .. event .. '="midnight.rpc(' .. js_string(id) .. ', event)"'
      end
    elseif key == 'children' or key == 'key' or (prelude and key == 'define:vars') or value == nil or value == false or type(value) == 'function' then
      -- skipped (functions are event handlers, which only mean something in
      -- the browser, see `mount`)
    elseif value == true then
      buf[#buf + 1] = ' ' .. key
    else
      if type(value) == 'table' and getmetatable(value) == nil then
        value = key == 'style' and style_value(value) or class_value(value)
      end
      buf[#buf + 1] = ' ' .. key .. '="' .. escape(value) .. '"'
    end
  end

  buf[#buf + 1] = '>'

  if VOID[tag] then
    for _, child in ipairs(node[3]) do
      if child ~= false and child ~= true then
        error('markup: <' .. tag .. '> can\'t have children', 0)
      end
    end
    return
  end

  -- text is rendered here: it doesn't need a path (only components do)
  local children, raw_text = node[3], RAW_TEXT[tag]
  if prelude then
    buf[#buf + 1] = prelude
  end
  if is_json_script(tag, props) then
    local empty = true
    for i = 1, #children do
      local child = children[i]
      if type(child) == 'string' and props.type == 'importmap' then
        buf[#buf + 1] = child -- (JSON text, written in the page)
        empty = false
      elseif child ~= false and child ~= true then
        buf[#buf + 1] = M.json(child)[1]
        empty = false
      end
    end
    if empty then
      -- `{x}` w/ x nil (h keeps nil children as false): null, so
      -- JSON.parse gets JSON
      buf[#buf + 1] = 'null'
    end
    buf[#buf + 1] = '</script>'
    return
  end
  for i = 1, #children do
    local child = children[i]
    if type(child) == 'string' then
      buf[#buf + 1] = raw_text and child or escape(child)
    elseif child ~= false and child ~= true then
      render_to(buf, child, raw_text, slot(path, i, child))
    end
  end
  buf[#buf + 1] = '</' .. tag .. '>'
end

-- A render's buffers are strings, and the buffers of child components (at
-- their frame's `children` indices). They're only joined at the end of the
-- render: until then, it isn't known whether the page has actions, and
-- `data-midnight` attributes (see `marked`) are only for pages that do.

-- flatten appends the strings of `buf` (and its child components') to `out`,
-- w/o the `data-midnight` attributes of the buffers in `skip` (if any).
local function flatten(buf, out, skip)
  local n = #out
  local attr = skip and skip[buf]
  for i = 1, #buf do
    local part = buf[i]
    if type(part) == 'table' then
      flatten(part, out, skip)
      n = #out
    elseif i ~= attr then
      n = n + 1
      out[n] = part
    end
  end
  return out
end

-- shell is `buf` w/ the html of child components (at `children` indices)
-- replaced by markers.
local shell = native.shell or function(buf, children)
  for i, id in pairs(children) do
    buf[i] = '<!--' .. id .. '-->'
  end
  local html = table.concat(buf)
  for i, id in pairs(children) do
    buf[i] = rendered[id].own
  end
  return html
end

-- join(buf, skip) returns the html of a buffer, w/o the `data-midnight`
-- attributes of the buffers in `skip` (if any, see flatten).
local join = native.join or function(buf, skip)
  return table.concat(flatten(buf, {}, skip))
end

-- html_of returns a component's html (w/ `data-midnight`, for `dispatch`).
local function html_of(entry)
  return join(entry.own)
end

local function render_component(buf, node, raw_text, path, owners)
  local id = path .. ':' .. component_id(node[1])
  seen[id] = true
  local instance = P.instances[id]
  if not instance then
    instance = { hooks = {} }
    P.instances[id] = instance
  end
  local entry = { parent = frame.id }
  rendered[id] = entry

  local parent, parent_frame = current, frame
  current = { instance = instance, hook = 0 }
  frame = { id = id, children = {}, next_action = 0 }
  local ok, result = pcall(node[1], node[2])
  current = parent
  if not ok then
    error(result, 0)
  end

  local ids = { id }
  for i, owner in ipairs(owners or {}) do
    ids[i + 1] = owner
  end
  local own = {}
  render_to(own, result, raw_text, id, ids)

  entry.own = own
  entry.shell = shell(own, frame.children)
  frame = parent_frame

  buf[#buf + 1] = own
  frame.children[#buf] = id
end

function render_to(buf, node, raw_text, path, owners)
  local t = type(node)

  if node == nil or node == false or node == true then
    return
  elseif t == 'string' then
    buf[#buf + 1] = raw_text and node or escape(node)
  elseif t == 'number' then
    buf[#buf + 1] = tostring(node)
  elseif t == 'table' then
    local mt = getmetatable(node)
    if mt == Node and type(node[1]) ~= 'string' then
      render_component(buf, node, raw_text, path, owners)
    elseif mt == Node then
      render_element(buf, node, path, owners)
    elseif mt == Raw or mt == Json then
      buf[#buf + 1] = node[1]
    elseif mt ~= nil and mt.__tostring then
      render_to(buf, tostring(node), raw_text, path)
    else -- list of children (ex. a fragment, or the result of `map`)
      for i = 1, #node do
        local child = node[i]
        if type(child) == 'string' then
          buf[#buf + 1] = raw_text and child or escape(child)
        elseif child ~= false and child ~= true then
          render_to(buf, child, raw_text, slot(path, i, child))
        end
      end
    end
  elseif t == 'function' then -- lazy child
    render_to(buf, node(), raw_text, path, owners)
  else
    error('markup: can\'t render a ' .. t, 0)
  end
end

local render_depth, nested_renders = 0, 0

-- render(node) renders a node (or string, list, etc.) to an HTML string.
function M.render(node)
  -- renders can nest (ex. `tostring(<b/>)` in a component), only the
  -- outermost one starts / finishes a render
  local outermost = render_depth == 0
  local root = 'r'
  local parent, parent_frame = current, frame
  if outermost then
    P.actions, seen, rendered, marked, nested_renders = {}, {}, {}, {}, 0
    libs, lib_seen = {}, {}
  else
    nested_renders = nested_renders + 1
    root = 'n' .. nested_renders
  end
  frame = { id = root, children = {}, next_action = 0 }

  render_depth = render_depth + 1
  local buf = {}
  local ok, err = pcall(render_to, buf, node, false, root)
  render_depth = render_depth - 1
  local root_frame = frame
  current, frame = parent, parent_frame
  if not ok then
    error(err, 0)
  end

  -- a nested render can't know yet whether the page has actions, so it
  -- keeps the attributes
  local html = join(buf, outermost and next(P.actions) == nil and marked or nil)
  if outermost then
    for id in pairs(P.instances) do
      if not seen[id] then
        P.instances[id] = nil
      end
    end
    P.last_render = { instances = rendered, shell = shell(buf, root_frame.children) }
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
local sheet_hashes = {}

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

local function css_rule(selector, rules, out)
  local declarations, nested = {}, {}
  for _, key in ipairs(sorted_keys(rules)) do
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
      css_rule(selector, rules[key], inner)
      out[#out + 1] = key .. ' { ' .. table.concat(inner, ' ') .. ' }'
    elseif refs:find('&', 1, true) then
      css_rule((refs:gsub('&', function() return selector end)), rules[key], out)
    else
      css_rule(selector .. ' ' .. refs, rules[key], out)
    end
  end
end

-- A top-level `@` key is an at-rule of its own, not a class: its keys are
-- the rule's own selectors, as written (`from`, `50%`, ...), ex.
-- `['@keyframes float'] = { from = { opacity = 0 }, to = { opacity = 1 } }`.
-- Its name isn't hashed, so pick one that won't collide.
local function is_at_rule(name)
  return name:sub(1, 1) == '@'
end

function M.css(rules)
  assert(type(rules) == 'table', 'markup: css requires a table of rules, by class name')
  local out = {}
  for _, name in ipairs(sorted_keys(rules)) do
    assert(type(rules[name]) == 'table', 'markup: css rules for ' .. name .. ' must be a table')
    if is_at_rule(name) then
      local inner = {}
      for _, selector in ipairs(sorted_keys(rules[name])) do
        css_rule(selector, rules[name][selector], inner)
      end
      out[#out + 1] = name .. ' { ' .. table.concat(inner, ' ') .. ' }'
    else
      css_rule('.' .. name .. '-' .. HASH, rules[name], out)
    end
  end
  local text = table.concat(out, '\n')
  local h = hash(text)

  if not sheet_hashes[h] then
    sheet_hashes[h] = true
    sheets[#sheets + 1] = (text:gsub(HASH, h))
  end

  local names = {}
  for _, name in ipairs(sorted_keys(rules)) do
    if not is_at_rule(name) then
      names[name] = name .. '-' .. h
    end
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

-- Custom elements: `elements(src, tags)` returns components for the custom
-- elements of a js module (a library), by name. They render their tag (w/
-- their props as attributes, and their children), and a page that uses any of
-- them loads the library once (`document` adds a module script to the <head>,
-- `scripts()` returns them), which registers all of its elements:
--
--   local { CopyButton } = elements('/static/elements.js', { CopyButton = 'got-copy' })
--   <CopyButton src={url}><button>copy</button></CopyButton>
--
-- The module exports each element's class under its name. A class w/ a static
-- `register(tag)` is registered w/ it, others w/ `customElements.define`.
-- Tags are valid custom element names (lowercase, w/ a `-`).
local REGISTER = "const r = (t, n) => { const C = m[n]; " ..
  "if (!C) throw new Error('elements: ' + n + ' (' + t + ') is not exported by ' + src); " ..
  "C.register ? C.register(t) : customElements.get(t) || customElements.define(t, C) };"

function M.elements(src, tags)
  assert(type(src) == 'string', 'markup: elements requires the url of a js module')
  assert(type(tags) == 'table', 'markup: elements requires a table of tags, by name')
  local lib = { src = src, tags = {} }
  local components = {}
  for _, name in ipairs(sorted_keys(tags)) do
    local tag = tags[name]
    if type(tag) ~= 'string' or not tag:match('^%l[%l%d._%-]*%-[%l%d._%-]*$') then
      error('markup: elements: ' .. tostring(name) .. ' = ' .. tostring(tag) ..
        ' is not a custom element name (lowercase, w/ a -)', 2)
    end
    lib.tags[#lib.tags + 1] = { name, tag }
    components[name] = function(props)
      if not lib_seen[lib] then
        lib_seen[lib] = true
        libs[#libs + 1] = lib
      end
      local attrs = {}
      for k, v in pairs(props) do
        if k ~= 'children' then
          attrs[k] = v
        end
      end
      return setmetatable({ tag, attrs, props.children or {} }, Node)
    end
  end
  return components
end

-- scripts() returns the module scripts that load the element libraries used
-- in the last render (or '' if none).
function M.scripts()
  local out = {}
  for _, lib in ipairs(libs) do
    local calls = {}
    for i, t in ipairs(lib.tags) do
      calls[i] = 'r(' .. js_string(t[2]) .. ', ' .. js_string(t[1]) .. ');'
    end
    out[#out + 1] = '<script type="module">import * as m from ' .. js_string(lib.src) ..
      '; const src = ' .. js_string(lib.src) .. '; ' .. REGISTER .. ' ' .. table.concat(calls, ' ') .. '</script>'
  end
  return table.concat(out)
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

local function vendored_morph()
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
-- by default w/ idiomorph (inlined in the page, see `vendored_morph`), a url
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
    morph = vendored_morph()
  elseif type(morph) == 'string' then
    morph = { src = morph }
  end
  P.mounted = { root = root, rpc = opts.rpc, headers = opts.headers or {}, morph = morph }
  return root()
end

-- Root elements that can't be replaced on their own (or be in a <template>).
local DOCUMENT_TAGS = { html = true, head = true, body = true }

-- last_index returns where the last `needle` is in `s` (a plain search, which is
-- much faster than a pattern anchored at the end).
local function last_index(s, needle)
  local last
  local i = s:find(needle, 1, true)
  while i do
    last = i
    i = s:find(needle, i + 1, true)
  end
  return last
end

-- A component's html is only needed to send what changed in a response,
-- comparing renders only needs their shells. So once a response is made, its
-- buffer is dropped.
local dispatching = false
local function forget_html()
  if P.last_render then
    for _, entry in pairs(P.last_render.instances) do
      entry.own = nil
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
    out[#out + 1] = '<template data-midnight-target="' .. escape(id) .. '">' .. html_of(after.instances[id]) .. '</template>'
  end
  return table.concat(out)
end

-- dispatch(id, data) runs an action w/ the event `data` sent by the client,
-- and returns what changed on the page as a response table ({ headers, body },
-- ex. for a rest handler): the components that changed (their html, by id),
-- or the whole page when something outside of a component (with its own root
-- element) changed. Nothing changed is an empty body.
function M.dispatch(id, data)
  assert(P.mounted, 'markup: dispatch requires a mounted page (see mount)')
  local headers = { ['Content-Type'] = 'text/html; charset=utf-8' }
  local action = P.actions[id]
  if not action then
    -- a stale action, ex. a second click on a button the first one removed:
    -- the client is out of date, so it gets the whole page
    return { headers = headers, body = M.document(P.mounted.root()) }
  end

  local before, sent = P.last_render, P.sent_sheets
  dispatching = true
  local ok, page = pcall(function()
    action(data or {})
    return M.document(P.mounted.root())
  end)
  dispatching = false
  if not ok then
    error(page, 0)
  end

  -- new styles (from `css` calls the page hasn't seen) need the whole page
  local body = before and #sheets == sent and changed(before, P.last_render) or page
  forget_html()
  return { headers = headers, body = body }
end

-- is_interactive returns whether the last render of a mounted page had any
-- actions (if not, the page is static: it never calls the server back, so
-- there's nothing to keep around for it).
function M.is_interactive()
  return P.mounted ~= nil and next(P.actions) ~= nil
end

-- session() is a page of its own: its own components' state and actions, so
-- one lua state can serve many pages at once (ex. a panel for each viewer),
-- instead of the one `mount` makes. Its actions are rendered as
-- `data-mn-<event>="id"` attributes (ex. `data-mn-click`), w/o a client
-- script: the host finds them, summarizes the event (like `mount`'s client:
-- `{ type, value, checked, key, form }`), and calls `dispatch`.
--
--   local s = session()
--   local html = s:render(() -> <App/>)  -- (again after each action)
--   s:dispatch(id, data)                  -- runs the action, false if stale
local Session = {}
Session.__index = Session

function M.session()
  return setmetatable({ page = new_page() }, Session)
end

-- render(root) renders `root()` (or `root`, a node) w/ this session's state,
-- and keeps it as the page's root.
function Session:render(root)
  if type(root) ~= 'function' then
    local node = root
    root = function() return node end
  end
  local page = self.page
  page.mounted = { root = root, attrs = 'data' }
  local previous = P
  P = page
  local ok, html = pcall(function() return M.render(root()) end)
  P = previous
  if not ok then
    error(html, 0)
  end
  return html
end

-- rerender() renders the root of the last `render` again.
function Session:rerender()
  assert(self.page.mounted, 'markup: rerender requires a rendered session')
  return self:render(self.page.mounted.root)
end

-- dispatch(id, data) runs the action `id` (from the last render) w/ the
-- event `data`. Returns false if there's no such action (ex. the client is
-- out of date). Render again for what changed.
function Session:dispatch(id, data)
  local action = self.page.actions[id]
  if not action then
    return false
  end
  action(data or {})
  return true
end

-- is_interactive returns whether the last render had any actions.
function Session:is_interactive()
  return next(self.page.actions) ~= nil
end

-- document(node) is like render, but prefixed w/ `<!DOCTYPE html>`. For a
-- mounted page w/ actions, it includes the client script.
function M.document(node)
  local html = M.render(node)
  local styles = M.styles() .. M.scripts()
  if styles ~= '' then
    local head_end = html:find('</head>', 1, true)
    local _, html_start = html:find('^%s*<html[^>]*>')
    if head_end then
      html = html:sub(1, head_end - 1) .. styles .. html:sub(head_end)
    elseif html_start then -- <html> w/o a <head>
      html = html:sub(1, html_start) .. '<head>' .. styles .. '</head>' .. html:sub(html_start + 1)
    else
      html = styles .. html
    end
  end
  html = '<!DOCTYPE html>' .. html
  if P.mounted then
    P.sent_sheets = #sheets
    if not dispatching then
      forget_html()
    end
  end
  if not M.is_interactive() then
    return html
  end

  local headers = {}
  for _, key in ipairs(sorted_keys(P.mounted.headers)) do
    headers[#headers + 1] = js_string(key) .. ': ' .. js_string(P.mounted.headers[key])
  end
  local rpc = P.mounted.rpc and js_string(P.mounted.rpc) or 'null'
  local cfg = '{ rpc: ' .. rpc .. ', headers: { ' .. table.concat(headers, ', ') .. ' } }'
  local script = '<script>' .. CLIENT:format(cfg) .. '</script>'
  if P.mounted.morph and P.mounted.morph.src then
    script = '<script src="' .. escape(P.mounted.morph.src) .. '"></script>' .. script
  elseif P.mounted.morph then
    script = '<script>' .. P.mounted.morph.inline .. '</script>' .. script
  end
  local body_end = last_index(html, '</body>') or last_index(html, '</html>')
  if body_end then
    return html:sub(1, body_end - 1) .. script .. html:sub(body_end)
  end
  return html .. script
end

M.escape = escape

Node.__tostring = M.render
Raw.__tostring = function(self) return self[1] end
Json.__tostring = function(self) return self[1] end

return M

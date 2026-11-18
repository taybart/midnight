// <todo-list api="..." done-class="...">: the todo list, in the browser. The
// server renders its markup (the form, the todos, a <template> of one todo,
// the counts) and this wires it up: changes go to the JSON api, which
// responds with the whole list, and the list is rebuilt from the template.
//
// Attributes: api (the api's url, relative to the page), done-class (the
// scoped class of a done todo's title).
export default class TodoList extends HTMLElement {
  connectedCallback() {
    this.form = this.querySelector('form')
    this.list = this.querySelector('ul')
    this.item = this.querySelector('template').content.firstElementChild
    this.addEventListener('submit', this.onSubmit)
    this.addEventListener('change', this.onChange)
    this.addEventListener('click', this.onClick)
  }

  disconnectedCallback() {
    this.removeEventListener('submit', this.onSubmit)
    this.removeEventListener('change', this.onChange)
    this.removeEventListener('click', this.onClick)
  }

  onSubmit = async (e) => {
    e.preventDefault()
    const title = this.form.elements.title
    if (await this.call('POST', '', { title: title.value })) {
      title.value = ''
    }
  }

  onChange = (e) => {
    const li = e.target.closest('li[data-id]')
    if (li && e.target.type === 'checkbox') {
      this.call('POST', `/${li.dataset.id}/toggle`)
    }
  }

  onClick = (e) => {
    const li = e.target.closest('li[data-id]')
    if (li && e.target.closest('[data-remove]')) {
      this.call('DELETE', `/${li.dataset.id}`)
    }
  }

  // call calls the api, and shows the list it responds with (or its error).
  // It returns whether it worked.
  async call(method, path, body) {
    let res, data
    try {
      res = await fetch(this.getAttribute('api') + path, {
        method,
        headers: body ? { 'Content-Type': 'application/json' } : {},
        body: body && JSON.stringify(body),
      })
      data = await res.json()
    } catch (err) {
      data = { error: err.message }
    }
    const error = this.querySelector('[data-error]')
    error.hidden = !data.error
    error.textContent = data.error ?? ''
    if (!res?.ok) return false
    this.render(data)
    return true
  }

  render(todos) {
    const done = this.getAttribute('done-class')
    const items = todos.map((t) => {
      const li = this.item.cloneNode(true)
      li.dataset.id = t.id
      li.querySelector('input').checked = t.done
      const title = li.querySelector('[data-title]')
      title.textContent = t.title
      title.classList.toggle(done, t.done)
      return li
    })
    const empty = this.querySelector('[data-empty]')
    empty.hidden = todos.length > 0
    this.list.replaceChildren(empty, ...items)
    const left = todos.filter((t) => !t.done).length
    this.querySelector('[data-left]').textContent = left
    this.querySelector('[data-done]').textContent = todos.length - left
  }
}

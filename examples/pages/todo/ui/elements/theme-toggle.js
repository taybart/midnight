// <theme-toggle>: wires up the first button in it (rendered by the server)
// to switch <body>'s data-theme (tb.css themes by it).
export default class ThemeToggle extends HTMLElement {
  connectedCallback() {
    this.button = this.querySelector('button')
    this.button?.addEventListener('click', this.toggle)
  }

  disconnectedCallback() {
    this.button?.removeEventListener('click', this.toggle)
  }

  toggle = () => {
    const dark = document.body.dataset.theme !== 'light'
    document.body.dataset.theme = dark ? 'light' : 'dark'
    this.button.textContent = dark ? '☾' : '☀'
  }
}

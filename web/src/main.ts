import { mount } from 'svelte'
import App from './App.svelte'
import './app.css'
import { themeState } from './lib/theme.svelte'

themeState.init()

mount(App, { target: document.getElementById('app')! })

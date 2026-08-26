---
name: shadcn-templ
description: Use when adding, modifying, or debugging shadcn-templ, templUI, templ components, Tailwind CSS, or frontend UI in this Go application.
---

# shadcn-templ

Before implementing UI changes, fetch and consult the current documentation index:

https://shadcn-templ.com/llms.txt

Open the linked installation, CLI, or component documentation relevant to the task. Follow the current documentation rather than relying on remembered APIs.

## Project Conventions

- Use templ for server-rendered HTML.
- Prefer shadcn-templ components over custom replacements.
- Keep client-side JavaScript minimal; use the component's documented vanilla JavaScript behavior when interactivity requires it.
- Preserve accessibility and large touch targets for the 16-inch touchscreen display.
- Ensure pages remain usable on desktop and mobile.
- Run templ generation, formatting, and the project's Go tests after making changes.

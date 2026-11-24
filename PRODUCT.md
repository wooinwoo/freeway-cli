# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Astro and Starlight with static output for documentation. The product itself is a native Go CLI/TUI and native Go provider integrations. Node.js is a documentation build dependency only.

## Users

Individual developers using their own accounts and free model allowances on their own computers. Documentation readers need to understand installation, account connection, model selection, and recovery from errors.

## Product Purpose

Freeway provides a personal local client for supported model APIs and official CLIs. The project is intended for noncommercial operation, with no paid subscription or hosted inference resale operated by the project.

## Operating Context

The public repository is github.com/wooinwoo/freeway-cli. Existing provider guides, CLI behavior, and tests are the content authority. The common Go backend ships as the local gateway `freewayd`; the client uses the documented HTTP boundary and remains independently testable.

## Capabilities and Constraints

The delivery covers TUI, provider integrations, documentation pages, and installation/updates. The final product has no Node.js or POC runtime dependency. Documentation and generated HTML remain local until publication is explicitly authorized. English-first bilingual documentation and Windows/Linux/macOS packaging are provisional defaults pending the user's optional preference response; execution support must be distinguished from cross-compilation.

## Brand Commitments

Freeway is the established name. Display the text wordmark without an adjacent icon; the user will design and supply a replacement icon later. The user explicitly requires following OpenRouter's actual documentation design, including its two-row navigation, typography, content spacing, quiet sidebars, tabbed code surfaces, and dark default. The user selected emerald green for the final accent after comparing green and blue. A stock Starlight appearance is rejected. Preserve readable, task-focused documentation and truthful Freeway content. Show actual installation commands prominently before overview tables.

## Evidence on Hand

The native code and tests live in cli/ and backend/internal/providers/. Historical POC files are reference material. Do not invent download availability, successful vendor probes, supported OS execution results, testimonials, or adoption numbers.

## Product Principles

- Users retain their own accounts and credentials.
- Explain actual model routes, free conditions, and unknown usage accurately.
- Preserve drafts and saved conversations through ordinary client actions.
- Document the commands that the distributed binary really implements.
- Keep backend policy and persistent user data outside the TUI. Known deviation: MCP server settings, image tool settings, and image OAuth tokens are currently stored by the client (`cli/imagegen`) in the user configuration directory until they move into the gateway.

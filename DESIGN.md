---
name: Freeway documentation
description: OpenRouter documentation rhythm with Freeway identity and truthful task-focused content.
colors:
  primary: "#34d399"
  primary-text: "#6ee7b7"
  active-surface: "#0c251e"
  canvas: "#060b0e"
  code-surface: "#0b1013"
  subtle-surface: "#161a1d"
  border: "#24272a"
  text-heading: "#e2dfe5"
  text-strong: "#d1cfd5"
  text-body: "#a29fa5"
  text-muted: "#8d8b91"
  light-primary: "#047857"
  light-primary-text: "#065f46"
  light-active-surface: "#ecfdf5"
  light-canvas: "#ffffff"
  light-code-surface: "#fafafa"
  light-subtle-surface: "#f0f0f2"
  light-border: "#e4e4e7"
  light-text-heading: "#18181b"
  light-text-strong: "#3f3f46"
  light-text-body: "#62626a"
  light-text-muted: "#71717a"
typography:
  headline:
    fontFamily: "'Plus Jakarta Sans Variable', 'Segoe UI', 'Malgun Gothic', sans-serif"
    fontSize: "36px"
    fontWeight: 600
    lineHeight: "40px"
    letterSpacing: "-0.04em"
  title:
    fontFamily: "'Plus Jakarta Sans Variable', 'Segoe UI', 'Malgun Gothic', sans-serif"
    fontSize: "24px"
    fontWeight: 600
    lineHeight: "32px"
    letterSpacing: "-0.035em"
  subtitle:
    fontFamily: "'Plus Jakarta Sans Variable', 'Segoe UI', 'Malgun Gothic', sans-serif"
    fontSize: "18px"
    lineHeight: "28px"
  body:
    fontFamily: "'Plus Jakarta Sans Variable', 'Segoe UI', 'Malgun Gothic', sans-serif"
    fontSize: "16px"
    fontWeight: 400
    lineHeight: "28px"
  label:
    fontFamily: "'Plus Jakarta Sans Variable', 'Segoe UI', 'Malgun Gothic', sans-serif"
    fontSize: "14px"
    lineHeight: "22px"
  code:
    fontFamily: "'Geist Mono Variable', ui-monospace, monospace"
    fontSize: "0.875rem"
    lineHeight: "1.7"
rounded:
  inline-code: "5px"
  navigation: "8px"
  button: "10px"
  control: "12px"
  container: "14px"
spacing:
  small: "8px"
  medium: "16px"
  inset: "20px"
  group: "24px"
  section: "32px"
  content-top: "40px"
  chrome: "48px"
components:
  copy-page:
    textColor: "{colors.text-strong}"
    rounded: "{rounded.button}"
    padding: "7px 11px"
  search-trigger:
    textColor: "{colors.text-body}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "38px"
  navigation-current:
    backgroundColor: "{colors.active-surface}"
    textColor: "{colors.primary-text}"
    rounded: "{rounded.navigation}"
    padding: "7px 16px"
  code-tabs:
    backgroundColor: "{colors.code-surface}"
    rounded: "{rounded.container}"
  callout:
    backgroundColor: "{colors.code-surface}"
    textColor: "{colors.text-body}"
    rounded: "{rounded.container}"
    padding: "16px 20px"
---

# Design System: Freeway documentation

## Overview

**Creative North Star: "OpenRouter's documentation rhythm, Freeway's identity"**

The visual authority is the actual [OpenRouter quickstart](https://openrouter.ai/docs/quickstart), as explicitly selected by the user. Preserve its quiet reading environment, two-row navigation, compact headings, and tabbed code surfaces. The user rejected the stock Starlight appearance. Freeway retains its name, navigation, and truthful product content. The header currently uses the text-only `freeway` wordmark. The user removed the adjacent icon and will provide its replacement later; do not display a brand icon until then.

This records the implemented documentation system in `docs/site`, not a design specification for the native TUI. Astro and Starlight retain content routing, bilingual pages, search, content components, and responsive menu infrastructure. Custom Header, Sidebar, PageTitle, ThemeProvider, and ThemeSelect components define the shell. Applying this system must preserve the documented HTTP client boundary and backend-last implementation plan; visual work must not move backend policy or persistent user data into the client.

**Key Characteristics:**
- Near-black neutral surfaces, restrained emerald green emphasis, and a dark default.
- Readable prose, compact navigation, fine borders, and flat containers.
- Plus Jakarta Sans for interface and reading; Geist Mono for code.
- Equivalent English and Korean structures with responsive navigation.

## Colors

Emerald green establishes orientation within neutral reading surfaces; the palette is not a green-tinted theme. Frontmatter values are normative and come from `docs/site/src/styles/custom.css`.

### Primary

- **Signal Green** (`primary`): accent role and focus outlines.
- **Reading Green** (`primary-text`): current navigation, category labels, selected underlines, and hovered prose links.
- **Quiet Green Backing** (`active-surface`): current left-navigation row.
- **Light-mode Green** (`light-primary`, `light-primary-text`, `light-active-surface`): darker readable emphasis and pale selected backing on light surfaces.

### Neutral

- **Near-black Canvas** (`canvas`): continuous page, header, and sidebar background.
- **Code Ink** (`code-surface`): code and callouts, separated by tone.
- **Subtle Surface** (`subtle-surface`): hover backing and hairlines.
- **Fine Border** (`border`): controls, tables, code frames, and containers.
- **Pale Heading**, **Strong Text**, **Reading Gray**, and **Muted Gray** (`text-heading`, `text-strong`, `text-body`, `text-muted`): text hierarchy without large color blocks.
- Corresponding `light-*` neutrals provide a white canvas, near-white supporting surfaces, pale borders, and charcoal-to-gray text. Light mode is an explicit alternate, not the initial state.

**The Orientation Rule.** Reserve emerald green for orientation, selection, and interaction. Keep long prose and ordinary code containers neutral.

The user selected green as the final accent. Green applies to navigation, tabs, links, focus, and text selection in both themes. Temporary URL and session-storage comparison overrides have been removed.

## Typography

**Display and Body Font:** Plus Jakarta Sans Variable, with Segoe UI, Malgun Gothic, and sans-serif fallbacks.
**Code Font:** Geist Mono Variable, with ui-monospace and monospace fallbacks.

The pairing is compact and contemporary. Korean uses the established system fallback where Latin fonts do not supply glyphs. Headings use tight tracking and balanced wrapping, without a marketing display scale.

### Hierarchy

- **Headline:** the frontmatter role defines desktop page titles. Mobile uses (30px), line-height (38px), and weight (600).
- **Title:** the frontmatter role defines second-level headings. Mobile uses (22px) with line-height (30px). Third-level headings use (20px), line-height (28px), and weight (600).
- **Subtitle:** descriptions remain readable beneath the title using the subtitle role.
- **Body:** the body role fills the bounded reading column; do not widen it to absorb spare desktop space.
- **Label:** navigation and category labels use the label role; utilities and tabs use compact (13–14px) text.
- **Code:** Geist Mono supports scrollable examples and compact bordered inline code.

## Layout

The shell is centered within (1440px). Desktop navigation has a (64px) utility row and a (48px) section-tab row. The utility row places the Freeway wordmark left, search up to (360px) wide near the center, and GitHub, language, and theme controls right. At (64rem), this row uses a three-column grid.

At (50rem) and above, the left task navigation uses the established (20rem) sidebar. The main pane has (40px) top, (44px) horizontal, and (64px) bottom padding. The reading column is capped at (688px). At (80rem), the right section navigation occupies (344px). Between (50rem) and (79.99rem), the right rail is hidden.

Below (50rem), the utility row is followed by a (56px) breadcrumb row. Section tabs and desktop utilities disappear; the menu button opens Starlight's sidebar with its language controls. Search and theme remain in the top row with (44px) targets. Content uses (20px) side gutters. Copy page moves below the description, tables scroll horizontally, and pagination stacks.

The page header separates from content by (32px). Second-level headings begin after (42px); navigation groups separate by (30px). Preserve this breathing room rather than filling it with promotional panels.

## Elevation & Depth

The documentation shell is flat. Fine borders, slight tonal differences, and whitespace distinguish surfaces. Expressive Code frames and pagination explicitly remove shadows. Search retains Starlight's functional modal behavior. Ordinary content should not become raised dashboard cards.

**The Flat Reading Rule.** Code, callouts, navigation, and pagination use borders or tone before elevation.

## Shapes

Use gently rounded rectangles. Inline code has tight corners; navigation rows are modestly curved; buttons and search controls are softer; code tabs and callouts share broad container corners. The frontmatter records observed radius roles. One-pixel borders remain quiet. The header wordmark is text only. Do not display the previous arrow or invent a replacement brand icon before the user provides one.

## Components

### Header and navigation

`DocsHeader.astro` supplies desktop rows, the text-only `freeway` wordmark, and mobile breadcrumbs. `DocsSidebar.astro` renders real Starlight routes as grouped task links with consistent outline icons. Current links combine emerald green text, dark green backing, and weight (600); hover uses a neutral surface. The right table of contents stays quiet, using emerald green for its current section.

### Search and theme controls

Search remains Starlight's working search trigger and dialog, with a thin border, rounded rectangle, and desktop keyboard hint. Mobile presents an icon. `DocsThemeProvider.astro` applies dark before rendering unless a stored preference exists. `DocsThemeSelect.astro` offers Dark, Light, and System, persists under `starlight-theme`, and follows OS changes only for System.

### Page title and copy action

`DocsPageTitle.astro` pairs a emerald green category with a compact title and gray description. The outlined Copy page button sits beside the desktop title and moves into normal flow on mobile. It copies main page text and exposes localized success or failure through a polite live region.

### Code tabs and inline code

Starlight tabs wrap neutral code surfaces with one fine outer border and rounded frame. Compact labels use a thin emerald green underline for selection. Terminal titlebars are hidden. Geist Mono and Expressive Code copy behavior are retained. Inline code remains compact and bordered.

### Callouts, tables, and pagination

Callouts use a neutral code-toned surface, fine border, and restrained title weight. Tables use horizontal separators and plain text hierarchy. Pagination uses bordered rounded links without shadows. These patterns support tasks without competing with headings.

### Interaction and accessibility

Controls retain a (2px) accent focus outline with (4px) offset. Thin outline icons inherit state color. Preserve semantic navigation, current-page attributes, tab roles, accessible labels, and localized feedback. Reduced-motion users receive effectively immediate animation and transition durations; avoid decorative reading-surface motion.

## Do's and Don'ts

### Do:

- **Do** follow the actual OpenRouter documentation reference for shell, spacing, and reading rhythm while keeping Freeway's identity.
- **Do** preserve default dark, stored theme choice, English/Korean structure, and working Starlight search and content behavior.
- **Do** keep the reading column bounded and use the observed mobile breadcrumb/menu pattern.
- **Do** use neutral surfaces and restrained emerald green for current location and interactive state.
- **Do** preserve truthful Freeway commands, provider conditions, and the backend-last HTTP boundary.

### Don't:

- **Don't** restore the stock Starlight shell, previous IBM Plex Sans/JetBrains Mono pairing, or green-tinted dark surfaces.
- **Don't** copy OpenRouter branding, sign-in features, navigation claims, or product capabilities into Freeway.
- **Don't** introduce oversized marketing headings, decorative gradients, prominent shadows, or generic dashboard cards into the reading shell.
- **Don't** remove keyboard focus, localized labels, mobile language access, tab semantics, or functional search.

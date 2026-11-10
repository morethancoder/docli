---
title: Widget SDK
generator: test fixture
---

# Widget SDK

The Widget SDK draws widgets in chat. This file is a small llms.txt-style
fixture for docli tests.

## Install

Add the package with your package manager:

```sh
# this line is a shell comment, not a heading
npm install widget-sdk
```

## API

All calls return promises.

### createWidget

Creates a widget. Takes `options` and returns a `Widget`.

| Option | Type | Notes |
|---|---|---|
| title | string | shown on top |
| size | number | in pixels |

### destroyWidget

Removes a widget created by [createWidget](#createwidget).

## API

A second section with the same title, to test unique anchors and names.

Setext Heading
--------------

Text under a setext heading.

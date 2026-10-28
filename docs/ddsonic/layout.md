# Layout

ddsonic fills the terminal. On a taller terminal, every list shows more rows. On a wider one, long titles show in full, and rows line up as a table rather than stretching across the screen. Resizing while ddsonic runs reflows the screen at once.

## What you see

- **Height:** the library's lists (levels, search results), the queue, Up next, lyrics and track info get every row between the header and the key bar. The now-playing lines, seek bar and visualizer keep their heights; extra rows go to the list.
- **Short terminals:** the queue, and track info, lyrics or Up next over it, take rows from the visualizer (down to one row) until the list has 8. If that is not enough, the queue's key bar shows only its first lines, keeping at least 3 list rows. Below 40×10, ddsonic shows "Terminal too small" until the terminal grows.
- **Width up to 100 columns:** rows fill the width, with the detail (artist, year) or duration at the right edge.
- **Width over 100 columns:** a list's rows form a table. Titles take a column wide enough for 9 in 10 of the list's titles, and details start after it; a longer title pushes its own detail right instead of being cut. Track rows (an album's tracks, the queue, Up next) end where most of their rows end. Rows never get narrower than 100 columns. The queue's settings column stays at the right edge.

## Border

A rounded border runs around the screen from 56×16 up; below that it is left out to keep rows for the list. From 80×24 up, a rule also separates the list from the key bar, and a line divides the queue from its settings column. The border takes the place of the frame's outer padding, so it costs no list rows; the queue's rule costs one. To turn it off:

```toml
[ddsonic]
border = false
```

## How sizing works (for contributors)

- `WindowSizeMsg` (`ui/model/update.go`) stores the size and calls `recomputeLayout` (`ui/model/layout.go`), then re-clamps the scroll of the screen in front (`clampActiveScrollState`, `ui/model/scroll.go`). `View()` recomputes the layout too, so every frame is laid out for the current size.
- `recomputeLayout` picks a tier from the size: too small (under 40×10), minimal, compact (56×16 and up), or full (80×24 and up). It counts the rows the tier and screen draw above and below the body (the chrome), fits the key bar (`libFitKeyBar`), and leaves the rest to the body as `layout.bodyRows`. With the library enabled, a playback screen's visualizer gives rows to the list first when the body is short (`libVisualizerYield`).
- cliamp caps its lists at 12 or 24 rows unless its expanded mode (Ctrl+X) is on; with the library enabled the lists take the whole body.
- `bodySize()` (`ui/model/library_layout.go`) returns the body's width and rows: the list column beside the settings pane on the two-column playback screen, else the frame's inner width. The library's views size themselves from it. A detail view (track info, say) that wants to use its room reads `bodySize()` rather than combining `ui.PanelWidth` with row counts of its own.
- The border is decided with the tier (`libBorderOn`, `ui/model/library_frame.go`): the frame style swaps one cell of padding on each side for it, so the row and column counts do not change. The rule replaces the blank spacer row above the key bar (`libSpacerRule`); the two-column queue, which has no spacer, counts a row for it. cliamp's full tier without a visualizer counts one chrome row short, relying on the fit to cut the frame's blank bottom row when a status line shows; with the border that row is the border's bottom line, so it is counted.
- Row widths on wide terminals come from `listRowWidth` and `listColumn` (`library_layout.go`). A library level measures its rows once, when its entries arrive (`libFrame.setEntries`). The queue measures its tracks once per playlist change (`queueFit`, keyed by `Playlist.Revision`). Rendering narrows `ui.PanelWidth` per row with `ui.WithPanelWidth`, so cliamp's row formatters need no change.

## Limits

- The visualizer, settings column (22–30 columns) and now-playing lines keep their sizes on a large terminal; only lists grow.
- On a very wide terminal, the space right of a list's table stays empty; nothing is stretched to fill it.
- cliamp's own screens (provider browsers, pickers, playlist manager) keep cliamp's sizing.

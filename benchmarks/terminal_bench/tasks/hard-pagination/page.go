package main

// Paginate splits items into pages of size pageSize and returns the items on
// the requested page (1-indexed) together with the total number of pages.
//
// Contract:
//   - Pages are 1-indexed. totalPages must be the ceiling of
//     len(items)/pageSize (a partial last page still counts as a page).
//   - For a valid page, return the correct sub-slice of items for that page;
//     the last page may be a partial page (fewer than pageSize items) and
//     must not panic.
//   - For page < 1 or page > totalPages, return an empty slice (len 0) and
//     the correct totalPages. Must not panic.
//   - If pageSize <= 0, return an empty slice and totalPages == 0. Must not
//     panic or divide by zero.
//   - For empty items, totalPages == 0 and any page returns an empty slice.
//
// BUG: this implementation has two defects:
//  1. totalPages is computed with plain integer division (len(items) /
//     pageSize), so a partial last page is silently dropped from the count
//     (e.g. 5 items at pageSize 2 reports 2 pages instead of 3).
//  2. The slice bounds `items[start:end]` are never clamped to len(items),
//     so requesting the last (partial) page, or any out-of-range page,
//     slices past the end of the backing array and panics.
func Paginate(items []string, pageSize, page int) (pageItems []string, totalPages int) {
	totalPages = len(items) / pageSize

	start := (page - 1) * pageSize
	end := start + pageSize
	return items[start:end], totalPages
}

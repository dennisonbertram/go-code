package main

// Paginate splits items into pages of size pageSize and returns the items on
// the requested page (1-indexed) together with the total number of pages.
//
// Reference solution: computes totalPages as a ceiling division and clamps
// the slice bounds so partial last pages and out-of-range pages never panic.
func Paginate(items []string, pageSize, page int) (pageItems []string, totalPages int) {
	if pageSize <= 0 || len(items) == 0 {
		return []string{}, 0
	}

	totalPages = (len(items) + pageSize - 1) / pageSize

	if page < 1 || page > totalPages {
		return []string{}, totalPages
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], totalPages
}

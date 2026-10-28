//go:build devkey

package catalog

// Dev builds also accept catalogs signed with keys/catalog-dev.key (in the repository on purpose).
func init() {
	PublicKeys = append(PublicKeys, mustKey("6o6iHESeqHocBauOqLTODIPzX32sLrotLYwjEl78jmw="))
}

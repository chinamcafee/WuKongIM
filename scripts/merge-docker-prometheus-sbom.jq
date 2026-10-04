# Merge the separately cataloged embedded executable into the image SBOM.
# Its directory-source root is not an image component; omit that synthetic edge.
if ($child | length) != 1 or $child[0].bomFormat != "CycloneDX" or
   ($child[0].components | type) != "array" or
   ($child[0].components | length) == 0 then
  error("missing embedded Prometheus SBOM components")
else
  # Source builds report (devel) in Go build info. Preserve Syft's references
  # while recording the release identity and upstream source used by Docker.
  .components = ((.components + ($child[0].components | map(
    if .name == "github.com/prometheus/prometheus" then
      .version = $prometheus_version |
      .purl = ("pkg:golang/github.com/prometheus/prometheus@" + $prometheus_version) |
      .properties = ((.properties // []) + [{
        name: "wukongim:prometheus:upstream-revision", value: $prometheus_revision
      }])
    else . end
  ))) | unique_by(."bom-ref")) |
  .dependencies = (
    ((.dependencies // []) + (($child[0].dependencies // []) |
      map(select(.ref != $child[0].metadata.component."bom-ref")))) |
    group_by(.ref) |
    map(.[0] + {dependsOn: ([.[].dependsOn[]?] | unique)})
  )
end

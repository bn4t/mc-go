This generator measures the vanilla Java26.2 runtime's registry mappings for
axe stripping, copper scraping, and copper wax removal/application. It reads
block-to-block map entries through runtime reflection and registry names;
it does not inspect or translate method implementations.

From the repository root, with JDK25 and the unpacked vanilla libraries:

```sh
go run ./tools/gentooltransforms
```

The generated file records both the bundle and unpacked runtime SHA1. Output
is sorted and formatted deterministically. `-vanilla` and `-out` override the
input directory and output path. During concurrent oracle work, use the
repository's JVM flock convention around regeneration. This factual table
does not encode player permissions, interaction priority, or tool costs;
those require separate behavior measurements and differential controls.

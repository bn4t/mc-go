#!/usr/bin/env bash
# Run from the repository root with JDK 25 and the extracted vanilla bundle.
set -euo pipefail
classes=$(mktemp -d)
trap 'rm -rf "$classes"' EXIT
classpath=vanilla/versions/26.2/server-26.2.jar
while IFS= read -r library; do
  classpath+=":$library"
done < <(find vanilla/libraries -name '*.jar' -type f | sort)
javac -cp "$classpath" -d "$classes" tools/genkineticmetadata/KineticMetadata.java
java -cp "$classes:$classpath" KineticMetadata internal/server/testdata/vanilla-26.2/kinetic_weapon_metadata.tsv

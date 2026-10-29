# Default item armor attribute facts

Generate from the committed factual TSV, with Go only:

```sh
go run ./tools/genarmorattrs
```

Refresh the TSV from Java 26.2 data-generator item-component reports:

```sh
go run ./tools/genarmorattrs -reports vanilla/generated/reports/minecraft/components/item
```

The tool extracts default armor, armor toughness and knockback resistance
modifier facts for 41 items (88 rows). It retains modifier identifiers,
equipment slots and zero amounts. All these default rows use `add_value`;
the generator rejects other operations and unexpected slots instead of
silently changing their interpretation. The generated table contains facts
only; it does not evaluate attributes or choose equipment lifecycle policy.

The reports encode netherite's float resistance as the promoted double
`0.10000000149011612`, and zero toughness entries remain real modifiers.
Native command/packet controls cover copper, iron, diamond, netherite and
wolf body armor, including slot filtering and modifier removal/reapplication.
Other facts are from the item reports; this is not a runtime test of all items.

Bundle SHA256:
`cdacdfb25898de5e4b4b0e5ddcc2722f77067e46605709c2d886c000ebb63ec5`.
Inner jar SHA256:
`183c0499c5f855570ee487dd38e141a53f0121f83a0b07a3bac2d8b6698823e8`.
TSV SHA256:
`cdc00e0be61458b97748264e16f49de16e8106d994e66353fdff0089dd73bd4c`.
Generated table SHA256:
`65b0fb8c9c90bf6bedc93e0c86855bb32649e44c22dc5e496025d0d861087fa2`.

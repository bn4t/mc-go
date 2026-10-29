# Effect attribute facts

Official Minecraft Java26.2, JDK25.0.5+8. The Java probe calls public
`MobEffect.createModifiers(0, BiConsumer)` for every built-in effect, recording
attribute ID, modifier ID, amount, and operation. It does not access private
fields or extract implementation structure.

Bundle SHA1: `823e2250d24b3ddac457a60c92a6a941943fcd6a`.
Runtime SHA1: `86765a5899bd9c96461036a628796b4245715058`.

From repository root, with JDK25 and the bundled libraries:

```sh
TASK_CP="vanilla/versions/26.2/server-26.2.jar:$(rg --files vanilla/libraries -g '*.jar' | paste -sd: -)"
javac -d work -cp "$TASK_CP" tools/geneffectattrs/PlayerEffectAttributesProbe.java
java -cp "work:$TASK_CP" PlayerEffectAttributesProbe > work/effect-attributes-probe.log
```

Retain each tab-separated record following `FACT` (the game logger may prefix
the line) in `internal/server/testdata/vanilla-26.2/effect_attributes.tsv`, then:

```sh
go run ./tools/geneffectattrs
```

The generated header records the exact input SHA256. The table describes
modifier templates, not all effect behavior. Active durations and hidden
instances use the existing effect codec and player clock.

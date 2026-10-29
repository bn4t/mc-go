package blackbox

import (
	"archive/zip"
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// decorationOrderAgent is a java agent that logs the order in which the
// real server runs ChunkGenerator.applyBiomeDecoration (the feature
// step) per chunk, entry and exit, so mc-go can decorate in the same
// order. It edits nothing but the two log calls, through the JDK's
// java.lang.classfile API.
const decorationOrderAgent = `
import java.io.*;
import java.lang.classfile.*;
import java.lang.classfile.instruction.ReturnInstruction;
import java.lang.constant.*;
import java.lang.instrument.*;
import java.security.ProtectionDomain;

public class DecoOrderAgent {
  public static void premain(String args, Instrumentation inst) throws Exception {
    // args: <hook jar>;<log file>[;<trace spec>]. The hook must be
    // visible to the server's own class loader: its jar goes on the
    // bootstrap path. A trace spec (debugging) lists
    // class#method#slot entries, comma separated, whose argument at
    // that local slot is logged on entry.
    String[] a = args.split(";", 3);
    java.util.Map<String, java.util.Map<String, Integer>> traces = new java.util.HashMap<>();
    if (a.length > 2) for (String t : a[2].split(",")) {
      String[] f = t.split("#");
      traces.computeIfAbsent(f[0], k -> new java.util.HashMap<>()).put(f[1], Integer.parseInt(f[2]));
    }
    System.setProperty("decoorder.log", a[1]);
    inst.appendToBootstrapClassLoaderSearch(new java.util.jar.JarFile(a[0]));
    ClassDesc hook = ClassDesc.of("DecoOrderHook");
    MethodTypeDesc sig = MethodTypeDesc.of(ConstantDescs.CD_void, ConstantDescs.CD_Object);
    MethodTypeDesc tsig = MethodTypeDesc.of(ConstantDescs.CD_void, ConstantDescs.CD_String, ConstantDescs.CD_Object);
    inst.addTransformer(new ClassFileTransformer() {
      public byte[] transform(ClassLoader l, String name, Class<?> c, ProtectionDomain pd, byte[] b) {
        ClassFile cf = ClassFile.of();
        java.util.Map<String, Integer> tm = traces.get(name);
        if (tm != null) {
          return cf.transformClass(cf.parse(b), (cb, ce) -> {
            if (ce instanceof MethodModel mm && tm.containsKey(mm.methodName().stringValue())) {
              int slot = tm.get(mm.methodName().stringValue());
              String tag = mm.methodName().stringValue();
              cb.transformMethod(mm, MethodTransform.transformingCode(new CodeTransform() {
                public void atStart(CodeBuilder c) { c.ldc(tag); c.aload(slot); c.invokestatic(hook, "trace", tsig); }
                public void accept(CodeBuilder c, CodeElement e) { c.with(e); }
              }));
            } else cb.with(ce);
          });
        }
        if (!"net/minecraft/world/level/chunk/ChunkGenerator".equals(name)) return null;
        CodeTransform ct = new CodeTransform() {
          public void atStart(CodeBuilder cb) { cb.aload(2); cb.invokestatic(hook, "enter", sig); }
          public void accept(CodeBuilder cb, CodeElement e) {
            if (e instanceof ReturnInstruction) { cb.aload(2); cb.invokestatic(hook, "exit", sig); }
            cb.with(e);
          }
        };
        return cf.transformClass(cf.parse(b), ClassTransform.transformingMethodBodies(
            m -> m.methodName().equalsString("applyBiomeDecoration") && m.methodTypeSymbol().parameterCount() == 3, ct));
      }
    });
  }
}
`

const decorationOrderHook = `
import java.io.*;

public class DecoOrderHook {
  static PrintWriter out;
  public static synchronized void log(String what, Object chunk) {
    try {
      if (out == null) out = new PrintWriter(new FileWriter(System.getProperty("decoorder.log"), true));
      Object pos = chunk.getClass().getMethod("getPos").invoke(chunk);
      int x = (int) pos.getClass().getMethod("x").invoke(pos);
      int z = (int) pos.getClass().getMethod("z").invoke(pos);
      out.println(what + " " + x + " " + z);
      out.flush();
    } catch (Throwable t) { if (out != null) { out.println("err " + t); out.flush(); } }
  }
  static final ThreadLocal<String> cur = new ThreadLocal<>();
  public static void enter(Object chunk) { log("enter", chunk); cur.set(String.valueOf(chunk)); }
  public static void exit(Object chunk) { log("exit", chunk); cur.remove(); }
  public static synchronized void trace(String tag, Object o) {
    try {
      if (out == null) out = new PrintWriter(new FileWriter(System.getProperty("decoorder.log"), true));
      out.println("trace " + cur.get() + " " + tag + " " + o);
    } catch (Throwable t) {}
  }
}
`

// buildOrderAgent compiles the agent and returns the agent jar and the
// hook jar (kept apart so only the hook joins the bootstrap path).
func buildOrderAgent(t *testing.T) (string, string) {
	t.Helper()
	javac, err := exec.LookPath("javac")
	if err != nil {
		if j, err2 := exec.LookPath("java"); err2 == nil {
			javac = filepath.Join(filepath.Dir(j), "javac")
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "DecoOrderAgent.java")
	if err := os.WriteFile(src, []byte(decorationOrderAgent), 0o644); err != nil {
		t.Fatal(err)
	}
	src2 := filepath.Join(dir, "DecoOrderHook.java")
	os.WriteFile(src2, []byte(decorationOrderHook), 0o644)
	cmd := exec.Command(javac, "-d", dir, src, src2)
	cmd.Env = append(os.Environ(), "JAVA_TOOL_OPTIONS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("javac agent: %v\n%s", err, out)
	}
	mk := func(name string, manifest string, keep func(string) bool) string {
		jarPath := filepath.Join(dir, name)
		f, err := os.Create(jarPath)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		w, _ := zw.Create("META-INF/MANIFEST.MF")
		fmt.Fprint(w, "Manifest-Version: 1.0\r\n"+manifest+"\r\n")
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".class") || !keep(e.Name()) {
				continue
			}
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			w, _ := zw.Create(e.Name())
			w.Write(b)
		}
		zw.Close()
		f.Close()
		return jarPath
	}
	hook := func(n string) bool { return strings.HasPrefix(n, "DecoOrderHook") }
	agent := mk("decoorder.jar", "Premain-Class: DecoOrderAgent\r\n", func(n string) bool { return !hook(n) })
	return agent, mk("decohook.jar", "", hook)
}

// readDecorationOrder parses the agent log: the chunks in the order
// their decoration started, plus the pairs whose decorations overlapped
// in time.
func readDecorationOrder(t *testing.T, path string) (order [][2]int32, overlaps int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("decoration order log: %v", err)
	}
	defer f.Close()
	running := map[[2]int32]bool{}
	seen := map[[2]int32]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var what string
		var x, z int32
		if n, _ := fmt.Sscanf(sc.Text(), "%s %d %d", &what, &x, &z); n != 3 {
			continue
		}
		k := [2]int32{x, z}
		switch what {
		case "enter":
			for o := range running {
				if absInt(o[0]-x) <= 2 && absInt(o[1]-z) <= 2 {
					overlaps++
				}
			}
			running[k] = true
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		case "exit":
			delete(running, k)
		}
	}
	return order, overlaps
}

func absInt(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

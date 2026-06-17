import pathlib
p = pathlib.Path('../src/agent.go')
code = p.read_text(encoding='utf-8')

# Remove the duplicate old off + default
old = '''	default:
		a.send(chatID, "usage:\n/dcp — status\n/dcp on — enable\n/dcp off — disable\n/dcp reset — clear compressed ranges")
		a.cfg.DCP.Enabled = false
		a.send(chatID, "✅ DCP disabled")
	default:
		a.send(chatID, "usage:\n/dcp — status\n/dcp on — enable\n/dcp off — disable")
	}
}'''

new = '''	default:
		a.send(chatID, "usage:\n/dcp — status\n/dcp on — enable\n/dcp off — disable\n/dcp reset — clear compressed ranges")
	}
}'''

if old not in code:
    print("ERROR: not found")
else:
    code = code.replace(old, new, 1)
    p.write_text(code, encoding='utf-8')
    print("OK: fixed duplicate switch cases")

const devices = document.getElementById("devices");
const scanBtn = document.getElementById("scan");
const disonnectBtn = document.getElementById("disonnect");
const clearBtn = document.getElementById("clear");
const stopBtn = document.getElementById("stop");

const events = document.getElementById("events");
const evtSource = new EventSource("http://" + document.location.host + "/events");
const devicesMap = new Map()

const appendLog = (text) => {
	const p = document.createElement("p");
	p.innerText = "> " + text;
	Object.assign(p.style, {
		"margin": "0px",
		"padding": "5px",
		"fontSize": "10px",
	});
	events.appendChild(p);
}

appendLog("Logs:")

const appendDevice = (name, addr) => {
	const tr = document.createElement("tr");
	const tn = document.createElement("td");
	tn.innerText = name;
	const ta = document.createElement("td");
	ta.innerText = addr;
	const tb = document.createElement("td");
	const btn = document.createElement("button");
	btn.innerText = "Connect"
	Object.assign(btn.style, {
		"margin": "0px 5px",
	});
	btn.setAttribute("data-href", `/connect/${addr}`)
	addHrefListener(btn);
	tb.appendChild(btn);
	tr.appendChild(tn);
	tr.appendChild(ta);
	tr.appendChild(tb);
	devices.appendChild(tr);
}

evtSource.onmessage = (e) => {
	console.log("event: ", e);
};

evtSource.addEventListener("DEVICE", (e) => {
	const d = e.data.replaceAll('"','').split(";")
	if (d.length < 2) {
		console.log("[ERROR] Not enough device info -", d);
		return ;
	}
	const [addr, name] = d;
	if (devicesMap.get(addr)) {
		return;
	}
	devicesMap.set(addr, true)
	appendDevice(name, addr);
})

evtSource.addEventListener("INFO", (e) => {
	appendLog(e.data.replaceAll('"', ''));
})

evtSource.addEventListener("ERROR", (e) => {
	appendLog(e.data.replaceAll('"', ''));
})

evtSource.onerror = (e) => {
	console.log("[ERROR] ", e)
}

evtSource.onopen = (e) => {
	console.log("[INFO] ", e)
}

clearBtn.addEventListener("click", () => {
	events.innerHTML = ""
	appendLog("Logs:")
})

const addHrefListener = (btn) => {
	btn.addEventListener("click", async () => {
		const url = btn.getAttribute("data-href")
		if (!url) {
			return
		}
		await fetch(url)
	})
}

document.querySelectorAll("button").forEach(btn => {
	addHrefListener(btn);
})

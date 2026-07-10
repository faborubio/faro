// Dashboard de Faro: un gráfico por indicador contra la propia API
// (/api/{code}/history). Serie única por gráfico — unidades distintas jamás
// comparten eje. Specs de marcas y paleta: skill dataviz (validada CVD/contraste
// en ambos modos; ver template).
"use strict";

(function () {
  const DAYS_DAILY = 90; // ~60 puntos hábiles
  const DAYS_MONTHLY = 395; // 13 valores mensuales

  const darkMq = window.matchMedia("(prefers-color-scheme: dark)");
  const css = (name) =>
    getComputedStyle(document.documentElement).getPropertyValue(name).trim();

  const fmt = (unit) =>
    new Intl.NumberFormat("es-CL", unit === "%"
      ? { minimumFractionDigits: 1, maximumFractionDigits: 1 }
      : { maximumFractionDigits: 2 });

  const monthShort = ["ene", "feb", "mar", "abr", "may", "jun",
    "jul", "ago", "sep", "oct", "nov", "dic"];

  function tickLabel(iso, cadence) {
    const [y, m, d] = iso.split("-");
    return cadence === "monthly"
      ? monthShort[+m - 1] + " " + y.slice(2)
      : +d + " " + monthShort[+m - 1];
  }

  // Crosshair: la línea vertical encuentra la X por el lector (dataviz —
  // nadie apunta a una línea de 2px). Solo en gráficos de línea.
  const crosshair = {
    id: "crosshair",
    afterDraw(chart) {
      const active = chart.tooltip && chart.tooltip.getActiveElements();
      if (!active || !active.length || chart.config.type !== "line") return;
      const { ctx, chartArea } = chart;
      ctx.save();
      ctx.strokeStyle = css("--axis");
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(active[0].element.x, chartArea.top);
      ctx.lineTo(active[0].element.x, chartArea.bottom);
      ctx.stroke();
      ctx.restore();
    },
  };

  function isoDaysAgo(days) {
    const d = new Date();
    d.setDate(d.getDate() - days);
    return d.toISOString().slice(0, 10);
  }

  function buildChart(canvas, card, values) {
    const code = card.dataset.code;
    const cadence = card.dataset.cadence;
    const unit = card.dataset.unit;
    const color = css("--serie-" + code) || css("--serie-uf");
    const surface = css("--surface");
    const nf = fmt(unit);

    const labels = values.map((v) => tickLabel(v.date, cadence));
    const data = values.map((v) => v.value);
    // Barras para la variación mensual del IPC (discreta, con negativos y el
    // 0,0 legítimo de CASE-005 — una línea lo escondería); línea para el resto.
    const isBar = unit === "%";
    const last = data.length - 1;

    return new Chart(canvas, {
      type: isBar ? "bar" : "line",
      data: {
        labels,
        datasets: [{
          label: code,
          data,
          borderColor: color,
          backgroundColor: isBar ? color : color + "1a", // wash 10%
          borderWidth: 2,
          borderJoinStyle: "round",
          borderCapStyle: "round",
          fill: !isBar,
          // Sin puntos salvo el final: end-dot ≥8px con anillo del surface.
          pointRadius: data.map((_, i) => (i === last ? 4 : 0)),
          pointHoverRadius: 5,
          pointBackgroundColor: color,
          pointBorderColor: surface,
          pointBorderWidth: 2,
          maxBarThickness: 24,
          borderRadius: 4, // extremo del dato; la base queda recta
          borderSkipped: "start",
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        interaction: { mode: "index", intersect: false },
        plugins: {
          legend: { display: false }, // serie única: el título de la tarjeta ya la nombra
          tooltip: {
            backgroundColor: css("--ink"),
            titleColor: css("--plane"),
            bodyColor: css("--plane"),
            titleFont: { weight: "normal" },
            bodyFont: { weight: "bold" }, // el valor manda; la fecha acompaña
            displayColors: false,
            callbacks: {
              title: (items) => values[items[0].dataIndex].date,
              label: (item) => nf.format(item.parsed.y) + " " + unit,
            },
          },
        },
        scales: {
          x: {
            grid: { display: false },
            border: { color: css("--axis") },
            ticks: { color: css("--muted"), maxRotation: 0, autoSkip: true, maxTicksLimit: 6 },
          },
          y: {
            beginAtZero: isBar, // la barra parte del cero o miente
            grid: { color: css("--grid") },
            border: { display: false },
            ticks: { color: css("--muted"), maxTicksLimit: 5, callback: (v) => nf.format(v) },
          },
        },
      },
      plugins: [crosshair],
    });
  }

  function fillTable(card, values, unit) {
    const tbody = card.querySelector("tbody");
    tbody.replaceChildren();
    const nf = fmt(unit);
    for (let i = values.length - 1; i >= 0; i--) {
      const tr = document.createElement("tr");
      const date = document.createElement("td");
      date.textContent = values[i].date;
      const value = document.createElement("td");
      value.textContent = nf.format(values[i].value);
      tr.append(date, value);
      tbody.append(tr);
    }
  }

  const charts = new Map(); // canvas → Chart, para redibujar al cambiar el tema
  const series = new Map(); // card → values ya traídos

  async function loadCard(card) {
    const code = card.dataset.code;
    const days = card.dataset.cadence === "monthly" ? DAYS_MONTHLY : DAYS_DAILY;
    try {
      const resp = await fetch(
        "/api/" + encodeURIComponent(code) + "/history?desde=" + isoDaysAgo(days));
      if (!resp.ok) throw new Error("HTTP " + resp.status);
      const body = await resp.json();
      if (!body.values || !body.values.length) throw new Error("sin valores");
      series.set(card, body.values);
      fillTable(card, body.values, card.dataset.unit);
      draw(card);
    } catch (err) {
      card.querySelector(".nodata").style.display = "flex";
    }
  }

  function draw(card) {
    const canvas = card.querySelector("canvas");
    const old = charts.get(canvas);
    if (old) old.destroy();
    charts.set(canvas, buildChart(canvas, card, series.get(card)));
  }

  darkMq.addEventListener("change", () => {
    // El tema cambia los roles CSS: se redibuja con los mismos datos.
    for (const card of series.keys()) draw(card);
  });

  for (const card of document.querySelectorAll(".card[data-code]")) {
    loadCard(card);
  }
})();

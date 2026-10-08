"use client";

import Image from "next/image";
import { useState } from "react";
import { ArrowUpRight, FileCheck2, GitBranch, Pause, Play, Search } from "lucide-react";

const recordings = [
  { id: "multi-agent", title: "Multi-agent execution", icon: GitBranch, description: "Plan approval, Tool rounds, five completed stages, and per-attempt usage evidence." },
  { id: "knowledge", title: "Knowledge retrieval", icon: Search, description: "Index a runbook, retrieve source evidence, and inspect a cited answer and Context Manifest." },
  { id: "verification", title: "Completion verification", icon: FileCheck2, description: "A draft fails its completion checks; a separate corrected Run passes the same contract." }
];

export function ProductDemo() {
  const [selected, setSelected] = useState(recordings[0]);
  const [failed, setFailed] = useState(false);
  const [paused, setPaused] = useState(false);

  return (
    <section className="home-demo home-section" id="demo" aria-labelledby="demo-title">
      <div className="home-demo-inner">
        <header className="home-demo-heading">
          <div><p className="home-kicker">Product walkthroughs</p><h2 id="demo-title">From task to evidence.</h2></div>
          <a className="home-text-link" href="https://github.com/hoiyd/agentflow-platform/blob/main/docs/guides/demo.md">Demo guide <ArrowUpRight size={15} /></a>
        </header>
        <div className="home-demo-picker" role="group" aria-label="Product demonstrations">
          {recordings.map(recording => (
            <button key={recording.id} type="button" aria-pressed={selected.id === recording.id}
              onClick={() => {
                if (selected.id === recording.id) return;
                setSelected(recording);
                setFailed(false);
                setPaused(false);
              }}>
              <recording.icon size={16} />{recording.title}
            </button>
          ))}
        </div>
        <figure className="home-demo-recording">
          <picture key={selected.id}>
            <source media="(prefers-reduced-motion: reduce)" srcSet={`/demos/${selected.id}.webp`} />
            <Image src={`/demos/${selected.id}.${paused ? "webp" : "gif"}`} unoptimized width={2880} height={1800}
              alt={`${selected.title} recording`} aria-describedby="demo-description" onError={() => setFailed(true)} />
          </picture>
          <figcaption id="demo-description">
            <div className="home-demo-caption-label">
              <span>Recorded product demos</span>
              <button type="button" className="home-demo-playback" onClick={() => setPaused(!paused)}
                aria-label={paused ? "Play animation" : "Pause animation"} title={paused ? "Play animation" : "Pause animation"}>
                {paused ? <Play size={14} /> : <Pause size={14} />}
              </button>
            </div>
            <p>{selected.description}</p>
          </figcaption>
        </figure>
        {failed ? <p className="home-demo-error" role="alert">Recording unavailable. <a href={`https://github.com/hoiyd/agentflow-platform/blob/main/apps/web/public/demos/${selected.id}.gif`}>View original GIF <ArrowUpRight size={14} /></a></p> : null}
      </div>
    </section>
  );
}

import React from "react";

export function Banner() {
  const message = new URLSearchParams(window.location.search).get("message") ?? "";
  return <div dangerouslySetInnerHTML={{ __html: message }} />; // expect: codeye.javascript.html-injection
}

# OllamaUI
A graphical user interface for interacting with ollama models writte in Go and Fyne.

---
This is about keeping it simple and usable. It will search your LAN for Ollama instances. Make sure you launch Ollama to be LAN visible `OLLAMA_HOST="http://0.0.0.0:11434" ollama serve`.  
You can copy a message to the clipboard via right click on desktop or long tap on mobile.  
It will save the message history and the text in the text box between restarts among other things. You can, and should, clear the chat history once in a while in the settings (top right button).  
  
If you have any suggestions or improvements feel free to tell me.

---
![Mobile](https://github.com/user-attachments/assets/e18d9083-c51d-4695-b052-e2898c8404bb)
![Settings](https://github.com/user-attachments/assets/3cf150f8-91cf-453a-9e30-4388143c1bb6)  
Scroll-Along means how long the response can get before it stops scrolling along. If you set a small number it stops sooner and if you set a very large number it scrolls forever.  
![Desktop - Markdown and Normal Render](https://github.com/user-attachments/assets/813a2024-b66b-4e95-997d-f5e4d7114728)  
